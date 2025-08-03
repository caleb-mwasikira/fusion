package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/caleb-mwasikira/fusion/utils"
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

func init() {
	var help bool
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[ERROR] getting user's home dir; %v\n", err)
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

func start_gRPCServer(errorChan chan<- error) {
	address := fmt.Sprintf(":%v", port)
	log.Printf("Starting GRPC FUSE service on address; %v\n", address)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		errorChan <- err
		return
	}

	certFile := filepath.Join(utils.CertDir, "server.crt")
	keyFile := filepath.Join(utils.CertDir, "server.key")
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
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
				log.Printf("[ERROR] unmounting filesystem; %v\n", err)
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
			log.Printf("[ERROR] mounting FUSE filesystem; %v\n", err)

			numberFuseFails += 1
			if numberFuseFails >= MAX_FAILS {
				log.Fatalln("Too many attempts restarting failed FUSE filesystem")
			}
			go mountFileSystem(errorChan1)

		case err := <-errorChan2:
			log.Printf("[ERROR] running GRPC FUSE service; %v\n", err)

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
