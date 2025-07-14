package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/grpc"
)

var (
	debug        bool
	source, dest string

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
	flag.StringVar(&source, "source", "", "Source directory")
	flag.StringVar(&dest, "dest", filepath.Join(homeDir, "FAT_BOY"), "Directory where the source's contents appear.")
	flag.BoolVar(&help, "help", false, "Display help message.")
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	// Ensure that source path exists and is a directory
	stat, err := os.Stat(source)
	if err != nil {
		fmt.Printf("Source path not found; %v\n", err)
		fmt.Println("")

		flag.Usage()
		os.Exit(1)
	}
	if !stat.IsDir() {
		log.Fatalln("Source path is NOT a directory")
	}

	// Create destination directory
	err = os.MkdirAll(dest, 0777)
	if err != nil && !errors.Is(err, os.ErrExist) {
		log.Fatalf("Error creating destination directory; %v\n", err)
	}
}

func start_FUSE_FileSystem(errorChan chan<- error) {
	log.Printf("Mounting fuse filesystem %v\n", source)

	loopbackRoot, err := NewLoopbackRoot(source)
	if err != nil {
		errorChan <- fmt.Errorf("error creating loopback Root directory; %v", err)
		return
	}

	fuseServer, err = fs.Mount(
		dest,
		loopbackRoot,
		&fs.Options{
			MountOptions: fuse.MountOptions{
				AllowOther: true,
				Debug:      debug,
			},
		},
	)
	if err != nil {
		errorChan <- fmt.Errorf("mount fail: %v", err)
		return
	}
	fuseServer.Wait()
}

func main() {
	errorChan1 := make(chan error)

	go start_FUSE_FileSystem(errorChan1)

	const MAX_FAILS = 3
	numberFuseFails := 0

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

		default:
			time.Sleep(30 * time.Second)
		}
	}
}
