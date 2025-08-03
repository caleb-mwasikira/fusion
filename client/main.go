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

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var (
	command              string
	debug                bool
	remote               string
	realpath, mountpoint string
	username, password   string
	orgName, deptName    string

	fuseServer *fuse.Server
	grpcClient proto.FuseClient
	authToken  string
)

func init() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[ERROR] getting user's home dir; %v\n", err)
	}

	authFlag := flag.NewFlagSet("auth", flag.ExitOnError)
	authFlag.StringVar(&username, "username", "", "Name of the user connecting to remote")
	authFlag.StringVar(&password, "password", "", "Password of the user connecting to remote")
	authFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	runFlag := flag.NewFlagSet("run", flag.ExitOnError)
	runFlag.BoolVar(&debug, "debug", false, "Display FUSE debug logs to stdout.")
	runFlag.StringVar(&realpath, "realpath", "", "Physical directory where files are stored")
	runFlag.StringVar(&mountpoint, "mountpoint", filepath.Join(homeDir, "TALL_BOY"), "Virtual directory where files appear")
	runFlag.StringVar(&username, "username", "", "Name of the user connecting to remote")
	runFlag.StringVar(&password, "password", "", "Password of the user connecting to remote")
	runFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	createDirFlag := flag.NewFlagSet("create_dir", flag.ExitOnError)
	createDirFlag.StringVar(&orgName, "org", "", "Name of the organization to create")
	createDirFlag.StringVar(&deptName, "dept", "", "Name of the department to create")
	createDirFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	createUserFlag := flag.NewFlagSet("create_user", flag.ExitOnError)
	createUserFlag.StringVar(&username, "username", "", "Name of the user to create")
	createUserFlag.StringVar(&password, "password", "", "Password of the user to create")
	createUserFlag.StringVar(&orgName, "org", "", "Name of the organization the user belongs to")
	createUserFlag.StringVar(&deptName, "dept", "", "Name of the department the user belongs to")
	createUserFlag.StringVar(&remote, "remote", "", "Remote GRPC FUSE server.")

	var help bool
	flag.BoolVar(&help, "help", false, "Display help message")

	flag.Usage = func() {
		fmt.Printf("Usage of %v:\n", authFlag.Name())
		authFlag.PrintDefaults()
		fmt.Printf("\r\n")

		fmt.Printf("Usage of %v:\n", runFlag.Name())
		runFlag.PrintDefaults()
		fmt.Printf("\r\n")

		fmt.Printf("Usage of %v:\n", createDirFlag.Name())
		createDirFlag.PrintDefaults()
		fmt.Printf("\r\n")

		fmt.Printf("Usage of %v:\n", createUserFlag.Name())
		createUserFlag.PrintDefaults()
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
	case "create_dir":
		parseFlag(createDirFlag)
	case "create_user":
		parseFlag(createUserFlag)
	default:
		flag.Usage()
		log.Fatalln("Invalid command")
	}

	grpcClient = New_gRPC_Client()
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

	loopbackRoot, err := NewLoopbackRoot(realpath)
	if err != nil {
		errorChan <- fmt.Errorf("error creating loopback Root directory; %v", err)
		return
	}

	fuseServer, err = fs.Mount(
		mountpoint,
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
		log.Fatalln("-mountpoint directory does not exist")
	}

	// Before we mount the FUSE file system first lets
	// make sure we are authenticated with the remote server
	response, err := grpcClient.Auth(context.Background(), &proto.AuthRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		log.Fatalf("[ERROR] authenticating with remote; %v\n", err)
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
				log.Printf("[ERROR] unmounting filesystem; %v\n", err)
			}
		}

		os.Exit(1)
	}()

	for {
		// Restart FUSE filesystem whenever it fails
		select {
		case err := <-errorChan:
			log.Printf("[ERROR] mounting FUSE filesystem; %v\n", err)

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
	switch command {
	case "auth":
		response, err := grpcClient.Auth(context.Background(), &proto.AuthRequest{
			Username: username,
			Password: password,
		})
		if err != nil {
			log.Fatalf("[ERROR] authenticating with remote; %v\n", err)
		}
		log.Println(response.Token)

	case "run":
		runFileSystem()

	case "create_user":
		_, err := grpcClient.CreateUser(context.Background(), &proto.CreateUserRequest{
			Username: username,
			Password: password,
			OrgName:  orgName,
			DeptName: deptName,
		})
		if err != nil {
			log.Fatalf("[ERROR] creating user; %v\n", err)
		}

	case "create_dir":
		_, err := grpcClient.CreateOrg(context.Background(), &proto.CreateOrgRequest{
			OrgName:  orgName,
			DeptName: deptName,
		})
		if err != nil {
			log.Fatalf("[ERROR] creating organization; %v\n", err)
		}

	default:
		//
	}
}
