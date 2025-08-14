package main

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/lib/events"
	"github.com/caleb-mwasikira/fusion/lib/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

//go:embed certs/ca.crt
var CA_CERT_DATA []byte

// Returns an authenticated gRPC client
func new_gRPC_client() proto.FuseClient {
	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(CA_CERT_DATA)
	if !ok {
		log.Fatalln("[GRPC] Error loading custom CA cert file")
	}

	// Setup TLS connection
	transportCreds := credentials.NewClientTLSFromCert(certPool, "")
	conn, err := grpc.NewClient(
		remote,
		grpc.WithTransportCredentials(transportCreds),
	)
	if err != nil {
		log.Fatalf("[GRPC] Error creating GRPC channel; %v\n", err)
	}

	return proto.NewFuseClient(conn)
}

// Embeds authorization key in gRPC request metadata
func NewAuthenticatedCtx(ctx context.Context) context.Context {
	md := metadata.New(map[string]string{
		"authorization": authToken,
	})
	return metadata.NewOutgoingContext(ctx, md)
}

// Opens a stream with remote and listens for file events
func startRemoteObserver(ctx context.Context) {
	log.Println("[SYNC] Launching REMOTE_OBSERVER goroutine")

	ctx = NewAuthenticatedCtx(ctx)
	stream, err := grpcClient.ObserveFileChanges(ctx, &emptypb.Empty{})
	if err != nil {
		log.Printf("[SYNC] Error launching REMOTE_OBSERVER; %v\n", err)
		return
	}

outer:
	for {
		select {
		case <-ctx.Done():
			log.Printf("[SYNC] Exiting REMOTE_OBSERVER goroutine; %v\n", ctx.Err())
			return

		default:
			fileEvent, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Server terminated stream ??
					log.Printf("[SYNC] Exiting REMOTE_OBSERVER goroutine; %v\n", err)
					break outer
				}
				log.Printf("[SYNC] REMOTE_OBSERVER error; %v\n", err)
				return
			}

			go handleFileEvent(fileEvent)
		}

	}
}

func handleFileEvent(fileEvent *proto.FileEvent) {
	log.Printf("[SYNC] REMOTE_OBSERVER received fileEvent: %s\n", lib.PrintFileEvent(fileEvent))
	eventType := events.EventType(fileEvent.Event)

	switch eventType {
	case events.ADD_FILE:
		mode := os.FileMode(fileEvent.Mode)
		fullpath := filepath.Join(realpath, fileEvent.Path)

		if mode.IsDir() {
			err := os.MkdirAll(fullpath, mode)
			if err != nil {
				log.Printf("[SYNC] Error creating directory; %v\n", err)
			}
			return
		}

		if mode.IsRegular() {
			file, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, mode)
			if err != nil {
				log.Printf("[SYNC] Error creating file; %v\n", err)
				return
			}
			file.Close()
		}

	case events.MODIFY_FILE:
		remote := proto.DirEntry{
			Path: fileEvent.Path,
			Mode: fileEvent.Mode,
		}
		err := downloadFile(&remote)
		if err != nil {
			log.Printf("[SYNC] Error downloading file changes; %v\n", err)
		}

	case events.RENAME_FILE:
		oldpath := filepath.Join(realpath, fileEvent.Path)
		newpath := filepath.Join(realpath, fileEvent.NewPath)

		err := os.Rename(oldpath, newpath)
		if err != nil {
			log.Printf("[SYNC] Error handling RENAME file event; %v\n", err)
			return
		}

	case events.DELETE_FILE:
		path := filepath.Join(realpath, fileEvent.Path)
		err := os.Remove(path)
		if err != nil {
			log.Printf("[SYNC] Error handling DELETE file event; %v\n", err)
		}

	default:
		log.Println("[SYNC] Unregistered file event")
	}
}

func fetchRemoteEntries(ctx context.Context, path string) error {
	if strings.Contains(path, "Trash") {
		return nil
	}

	// Download directory tree and re-create it
	ctx = NewAuthenticatedCtx(ctx)
	response, err := grpcClient.ReadDirAll(ctx, &proto.DirEntry{
		Path: path,
	})
	if err != nil {
		return err
	}

	remoteEntries := response.GetEntries()
	wg := sync.WaitGroup{}

	for _, remoteEntry := range remoteEntries {
		mode := os.FileMode(remoteEntry.Mode)
		fullpath := filepath.Join(realpath, remoteEntry.Path)

		if mode.IsDir() {
			err := os.MkdirAll(fullpath, 0755)
			if err != nil {
				log.Printf("[SYNC] Error creating directory; %v\n", err)
			}
		}

		if mode.IsRegular() {
			wg.Add(1)
			go func(file *proto.DirEntry) {
				defer wg.Done()
				err := downloadFile(file)
				if err != nil {
					log.Printf("[SYNC] Error downloading remote file; %v\n", err)
				}
			}(remoteEntry)
		}
	}

	wg.Wait()

	return nil
}

func downloadFile(remote *proto.DirEntry) error {
	// log.Printf("[SYNC] Downloading remote file \"%v\"\n", remote.Path)

	fullpath := filepath.Join(realpath, remote.Path)
	file, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, os.FileMode(remote.Mode))
	if err != nil {
		return err
	}
	defer file.Close()

	// Remote is a file;
	// We need to check for any file changes on remote and
	// download them
	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return err
	}
	digest := hash.Sum(nil)
	localFileHash := hex.EncodeToString(digest)

	// Download file
	authCtx := NewAuthenticatedCtx(context.Background())
	stream, err := grpcClient.DownloadFile(
		authCtx,
		&proto.DownloadRequest{
			Path:         remote.Path,
			ExpectedHash: localFileHash,
		},
	)
	if err != nil {
		return err
	}

	totalExpectedSize := -1
	recvBytes := 0

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if totalExpectedSize == -1 {
			totalExpectedSize = int(chunk.TotalSize)
		}

		n, err := file.WriteAt(chunk.Data, chunk.Offset)
		if err != nil {
			return err
		}
		recvBytes += n
	}

	if totalExpectedSize == -1 || recvBytes == 0 {
		// No file received and no error means we have the same
		// local file as remote
		return nil
	}

	if totalExpectedSize != -1 && recvBytes != totalExpectedSize {
		return fmt.Errorf("expected file of size %v but got %v bytes instead", totalExpectedSize, recvBytes)
	}

	log.Printf("[SYNC] File \"%v\" updated successfully\n", remote.Path)
	return nil
}
