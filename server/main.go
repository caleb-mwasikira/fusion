package main

import (
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
)

var (
	debug                bool
	realPath, mountPoint string
	port                 uint

	fuseServer *fuse.Server
	grpcServer *grpc.Server
)

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
	if ok := utils.IsDirExists(realPath); !ok {
		log.Fatalln("-realpath directory does not exist")
	}

	// Ensure destination directory exists
	if ok := utils.IsDirExists(mountPoint); !ok {
		log.Fatalln("-mountpoint directory does not exist")
	}
}

func start_FUSE_FileSystem(errorChan chan<- error) {
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

func start_GRPC_FUSE_Service(errorChan chan<- error) {
	address := fmt.Sprintf(":%v", port)
	log.Printf("Starting GRPC FUSE service on address; %v\n", address)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		errorChan <- err
		return
	}

	grpcServer = grpc.NewServer()
	proto.RegisterFuseServiceServer(grpcServer, GrpcFuseService{
		path: mountPoint,
	})
	err = grpcServer.Serve(listener)
	if err != nil {
		errorChan <- err
	}
}

func main() {
	errorChan1 := make(chan error)
	errorChan2 := make(chan error)

	go start_FUSE_FileSystem(errorChan1)
	go start_GRPC_FUSE_Service(errorChan2)

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
			go start_FUSE_FileSystem(errorChan1)

		case err := <-errorChan2:
			log.Printf("Error running GRPC FUSE service; %v\n", err)

			numberGrpcFails += 1
			if numberFuseFails >= MAX_FAILS {
				log.Fatalln("Too many attempts restarting failed GRPC FUSE service")
			}
			go start_GRPC_FUSE_Service(errorChan2)

		default:
			time.Sleep(30 * time.Second)
		}
	}
}
