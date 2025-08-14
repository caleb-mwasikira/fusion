package main

import (
	"context"
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

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type key string

var (
	debug                bool
	realpath, mountpoint string
	port                 uint

	SECRET_KEY string
	fuseServer *fuse.Server
	grpcServer *grpc.Server
	userCtxKey key = "user"
)

func init() {
	var help bool
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting user's home dir; %v\n", err)
	}

	flag.BoolVar(&debug, "debug", false, "Display FUSE debug logs to stdout.")
	flag.StringVar(&realpath, "realpath", "", "Physical directory where files are stored")
	flag.StringVar(&mountpoint, "mountpoint", filepath.Join(homeDir, "FAT_BOY"), "Virtual directory where files appear")
	flag.UintVar(&port, "port", 1054, "Port to run the GRPC FUSE service on.")
	flag.BoolVar(&help, "help", false, "Display help message.")
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	err = lib.LoadEnv()
	if err != nil {
		log.Fatalf("Error loading env variables; %v\n", err)
	}

	// Ensure SECRET_KEY is always set
	SECRET_KEY = os.Getenv("SECRET_KEY")

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
	log.Printf("Mounting directory %v -> %v\n", realpath, mountpoint)

	// Ensure realpath directory exists
	if !dirExists(realpath) {
		log.Fatalln("-realpath directory does not exist")
	}

	// Ensure mountpoint directory exists
	if !dirExists(mountpoint) {
		log.Println("-mountpoint directory does not exist")
		err := os.Mkdir(mountpoint, 0755)
		if err != nil {
			log.Fatalf("Error creating mount directory; %v\n", err)
		}
	}

	fileSystem, err := NewFileSystem(realpath)
	if err != nil {
		errorChan <- fmt.Errorf("error creating loopback Root directory; %v", err)
		return
	}

	fuseServer, err = fs.Mount(
		mountpoint,
		fileSystem,
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

	// If we reach here the filesystem has been unmounted by user
	// exit program
	log.Fatalln("Filesystem unmounted by user")
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

	// Create new FuseServer instance
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fuseServer := NewFuseServer(ctx, mountpoint)
	proto.RegisterFuseServer(grpcServer, fuseServer)

	log.Printf("Starting GRPC server on address; %v\n", address)
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
