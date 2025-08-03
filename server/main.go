package main

import (
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type key string

var (
	debug                bool
	realPath, mountPoint string
	port                 uint

	fuseServer *fuse.Server
	grpcServer *grpc.Server
	userCtxKey key = "user"
)

//go:embed secret.txt
var SECRET_KEY string

func init() {
	var help bool
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting user's home dir; %v\n", err)
	}

	flag.BoolVar(&debug, "debug", false, "Display FUSE debug logs to stdout.")
	flag.StringVar(&realPath, "realpath", "", "Physical directory where files are stored")
	flag.StringVar(&mountPoint, "mountpoint", filepath.Join(homeDir, "FAT_BOY"), "Virtual directory where files appear")
	flag.UintVar(&port, "port", 1054, "Port to run the GRPC FUSE service on.")
	flag.BoolVar(&help, "help", false, "Display help message.")
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	// Ensure realPath directory exists
	if !dirExists(realPath) {
		log.Fatalln("-realpath directory does not exist")
	}

	// Ensure destination directory exists
	if !dirExists(mountPoint) {
		log.Fatalln("-mountpoint directory does not exist")
	}

	// Ensure SECRET_KEY is always set
	if strings.TrimSpace(SECRET_KEY) == "" {
		log.Fatalln("Missing SECRET_KEY env variable")
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func mountFileSystem(errorChan chan<- error) {
	log.Printf("Mounting directory %v -> %v\n", realPath, mountPoint)

	loopbackRoot, err := NewLoopbackRoot(realPath)
	if err != nil {
		errorChan <- fmt.Errorf("error creating loopback Root directory; %v", err)
		return
	}

	fuseServer, err = fs.Mount(
		mountPoint,
		loopbackRoot,
		&fs.Options{
			MountOptions: fuse.MountOptions{
				AllowOther: true,
				Debug:      debug,
			},
			UID: uint32(os.Geteuid()),
			GID: uint32(os.Getegid()),
		},
	)
	if err != nil {
		errorChan <- fmt.Errorf("mount fail: %v", err)
		return
	}
	fuseServer.Wait()
}

//go:embed certs/server.crt
//go:embed certs/server.key
var certDir embed.FS

func loadTLSCertificate() (tls.Certificate, error) {
	// Read certificate data
	certData, err := certDir.ReadFile("certs/server.crt")
	if err != nil {
		return tls.Certificate{}, err
	}

	// Read private key data
	keyData, err := certDir.ReadFile("certs/server.key")
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.X509KeyPair(certData, keyData)
}

func start_gRPCServer(errorChan chan<- error) {
	address := fmt.Sprintf(":%v", port)
	log.Printf("Starting GRPC FUSE service on address; %v\n", address)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		errorChan <- err
		return
	}

	tlsCert, err := loadTLSCertificate()
	if err != nil {
		errorChan <- err
		return
	}

	transportCreds := credentials.NewServerTLSFromCert(&tlsCert)
	grpcServer = grpc.NewServer(
		grpc.Creds(transportCreds),
		grpc.UnaryInterceptor(AuthInterceptor),
		grpc.StreamInterceptor(AuthStreamInterceptor),
	)
	proto.RegisterFuseServer(
		grpcServer,
		FuseServer{
			path: mountPoint,
		},
	)
	err = grpcServer.Serve(listener)
	if err != nil {
		errorChan <- err
	}
}

func main() {
	errorChan1 := make(chan error)
	errorChan2 := make(chan error)

	go mountFileSystem(errorChan1)
	go start_gRPCServer(errorChan2)

	const MAX_FAILS = 3
	numberFuseFails := 0
	numberGrpcFails := 0

	// Close servers when SIGINT and SIGTERM signals are received
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		if fuseServer != nil {
			log.Println("Unmounting filesystem now")
			err := fuseServer.Unmount()
			if err != nil {
				log.Printf("Error unmounting filesystem; %v\n", err)
			}
		}

		if grpcServer != nil {
			log.Println("Stopping GRPC FUSE service")
			grpcServer.Stop()
		}

		os.Exit(1)
	}()

	for {
		// Restart FUSE filesystem whenever it fails
		select {
		case err := <-errorChan1:
			log.Printf("Error mounting FUSE filesystem; %v\n", err)

			numberFuseFails += 1
			if numberFuseFails >= MAX_FAILS {
				log.Fatalln("Too many attempts restarting failed FUSE filesystem")
			}
			go mountFileSystem(errorChan1)

		case err := <-errorChan2:
			log.Printf("Error running GRPC FUSE service; %v\n", err)

			numberGrpcFails += 1
			if numberFuseFails >= MAX_FAILS {
				log.Fatalln("Too many attempts restarting failed GRPC FUSE service")
			}
			go start_gRPCServer(errorChan2)

		default:
			time.Sleep(30 * time.Second)
		}
	}
}
