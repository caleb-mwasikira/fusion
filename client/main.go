package main

import (
	"flag"
	"fmt"
	"log"
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
	"google.golang.org/grpc/credentials/insecure"
)

var (
	debug                bool
	realPath, mountPoint string
	remoteAddress        string

	fuseServer *fuse.Server
	grpcClient proto.FuseServiceClient
)

func init() {
	var help bool
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting user's home dir; %v\n", err)
	}

	flag.BoolVar(&debug, "debug", false, "Display FUSE debug logs to stdout.")
	flag.StringVar(&realPath, "realpath", "", "Physical directory where files are stored")
	flag.StringVar(&mountPoint, "mountpoint", filepath.Join(homeDir, "TALL_BOY"), "Virtual directory where files appear")
	flag.StringVar(&remoteAddress, "remote", "127.0.0.1:1054", "Remote GRPC FUSE server.")
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

	// Ensure mountPoint directory exists
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

func start_GRPC_Client() {
	log.Println("Creating GRPC client")
	conn, err := grpc.NewClient(
		remoteAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Error creating GRPC channel; %v\n", err)
	}

	grpcClient = proto.NewFuseServiceClient(conn)
}

func main() {
	start_GRPC_Client()

	errorChan1 := make(chan error)
	go start_FUSE_FileSystem(errorChan1)

	const MAX_FAILS = 3
	numberFails := 0

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

		os.Exit(1)
	}()

	for {
		// Restart FUSE filesystem whenever it fails
		select {
		case err := <-errorChan1:
			log.Printf("Error mounting FUSE filesystem; %v\n", err)

			numberFails += 1
			if numberFails >= MAX_FAILS {
				log.Fatalln("Mounting FUSE filesystem failed too many times")
			}
			go start_FUSE_FileSystem(errorChan1)

		default:
			time.Sleep(30 * time.Second)
		}
	}
}
