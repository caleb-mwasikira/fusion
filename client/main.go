package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var (
	command              string
	debug                bool
	remote               string
	realpath, mountpoint string
	email, password      string
	orgName, deptName    string

	fuseServer *fuse.Server
	grpcClient proto.FuseClient
	authToken  string
)

func init() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting user's home dir; %v\n", err)
	}

	authFlag := flag.NewFlagSet("auth", flag.ExitOnError)
	authFlag.StringVar(&email, "email", "", "Name of the user connecting to remote")
	authFlag.StringVar(&password, "password", "", "Password of the user connecting to remote")
	authFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	runFlag := flag.NewFlagSet("run", flag.ExitOnError)
	runFlag.BoolVar(&debug, "debug", false, "Display FUSE debug logs to stdout.")
	runFlag.StringVar(&realpath, "realpath", "", "Physical directory where files are stored")
	runFlag.StringVar(&mountpoint, "mountpoint", filepath.Join(homeDir, "TALL_BOY"), "Virtual directory where files appear")
	runFlag.StringVar(&email, "email", "", "Name of the user connecting to remote")
	runFlag.StringVar(&password, "password", "", "Password of the user connecting to remote")
	runFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	var help bool
	flag.BoolVar(&help, "help", false, "Display help message")

	flag.Usage = func() {
		fmt.Printf("Usage of %v:\n", authFlag.Name())
		authFlag.PrintDefaults()
		fmt.Printf("\r\n")

		fmt.Printf("Usage of %v:\n", runFlag.Name())
		runFlag.PrintDefaults()
		fmt.Printf("\r\n")

		fmt.Printf("Common arguments:\n")
		flag.PrintDefaults()
	}

	if help {
		flag.Usage()
		os.Exit(0)
	}

	if len(os.Args) < 2 {
		flag.Usage()
		log.Fatalln("Expected at least one command")

	}

	command = os.Args[1]
	switch command {
	case "auth":
		parseFlag(authFlag)
	case "run":
		parseFlag(runFlag)
	default:
		flag.Usage()
		log.Fatalln("Invalid command")
	}

	grpcClient = new_gRPC_client()
}

func parseFlag(flagSet *flag.FlagSet) {
	flagSet.Parse(os.Args[2:])
	flagSet.VisitAll(func(f *flag.Flag) {
		value := f.Value.String()
		if strings.TrimSpace(value) == "" {
			log.Fatalf("Missing flag value -%v\n", f.Name)
		}
	})
}

func mountFileSystem(errorChan chan<- error) {
	log.Printf("Mounting directory %v -> %v\n", realpath, mountpoint)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fileSystem, err := NewFileSystem(ctx, realpath)
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func runFileSystem() {
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

	// Before we mount the FUSE file system first lets
	// make sure we are authenticated with the remote server
	response, err := grpcClient.Auth(context.Background(), &proto.AuthRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		log.Fatalf("Error authenticating with remote; %v\n", err)
	}
	authToken = response.Token

	errorChan := make(chan error)
	go mountFileSystem(errorChan)

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
		case err := <-errorChan:
			log.Printf("Error mounting FUSE filesystem; %v\n", err)

			numberFails += 1
			if numberFails >= MAX_FAILS {
				log.Fatalln("Mounting FUSE filesystem failed too many times")
			}
			go mountFileSystem(errorChan)

		default:
			time.Sleep(30 * time.Second)
		}
	}
}

func main() {
	defer func() {
		// recover() will return a non-nil value if a panic occurred.
		if r := recover(); r != nil {
			log.Printf("A panic occurred: %v. Exiting gracefully.", r)
			// You can perform cleanup actions here.
			// For example, closing files or network connections.
			os.Exit(1) // Exit with a non-zero status to indicate an error.
		}
	}()

	switch command {
	case "auth":
		response, err := grpcClient.Auth(context.Background(), &proto.AuthRequest{
			Email:    email,
			Password: password,
		})
		if err != nil {
			log.Fatalf("Error authenticating with remote; %v\n", err)
		}
		log.Println(response.Token)

	case "run":
		runFileSystem()

	default:
		//
	}
}
