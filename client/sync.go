package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/caleb-mwasikira/fusion/lib/events"
	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Opens a stream with remote and listens for file events
func startRemoteObserver(ctx context.Context) {
	log.Println("[INFO] Launching REMOTE_OBSERVER goroutine")

	ctx = NewAuthenticatedCtx(ctx)
	stream, err := grpcClient.ObserveFileChanges(ctx, &emptypb.Empty{})
	if err != nil {
		log.Printf("[ERROR] launching REMOTE_OBSERVER; %v\n", err)
		return
	}

outer:
	for {
		select {
		case <-ctx.Done():
			log.Printf("Exiting REMOTE_OBSERVER goroutine; %v\n", ctx.Err())
			return

		default:
			fileEvent, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Server terminated stream ??
					log.Printf("Exiting REMOTE_OBSERVER goroutine; %v\n", err)
					break outer
				}
				log.Printf("REMOTE_OBSERVER error; %v\n", err)
				return
			}

			go handleFileEvent(fileEvent)
		}

	}
}

func handleFileEvent(fileEvent *proto.FileEvent) {
	log.Printf("REMOTE_OBSERVER received fileEvent: %s\n", fileEvent)
	eventType := events.EventType(fileEvent.Event)

	switch eventType {
	case events.ADD_FILE:
		mode := os.FileMode(fileEvent.Mode)
		fullpath := filepath.Join(mountpoint, fileEvent.Path)

		if mode.IsDir() {
			err := os.MkdirAll(fullpath, mode)
			if err != nil {
				log.Printf("Error creating directory; %v\n", err)
			}
			return
		}

		if mode.IsRegular() {
			file, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, mode)
			if err != nil {
				log.Printf("Error creating file; %v\n", err)
				return
			}
			file.Close()
		}

	case events.MODIFY_FILE:
		remote := proto.DirEntry{
			Path: fileEvent.Path,
			Mode: fileEvent.Mode,
		}
		err := downloadAndSave(&remote)
		if err != nil {
			log.Printf("Error downloading file changes; %v\n", err)
		}

	case events.RENAME_FILE:
		oldpath := filepath.Join(mountpoint, fileEvent.Path)
		newpath := filepath.Join(mountpoint, fileEvent.NewPath)

		err := os.Rename(oldpath, newpath)
		if err != nil {
			log.Printf("Error handling RENAME file event; %v\n", err)
			return
		}

	case events.DELETE_FILE:
		path := filepath.Join(mountpoint, fileEvent.Path)
		err := os.Remove(path)
		if err != nil {
			log.Printf("Error handling DEL file event; %v\n", err)
		}

	default:
		log.Println("Unregistered file event")
	}
}

func fetchRemoteEntries(ctx context.Context, path string) ([]fuse.DirEntry, error) {
	if strings.Contains(path, "Trash") {
		return []fuse.DirEntry{}, nil
	}

	// Download directory tree and re-create it
	ctx = NewAuthenticatedCtx(ctx)
	response, err := grpcClient.ReadDirAll(ctx, &proto.DirEntry{
		Path: path,
	})
	if err != nil {
		return nil, err
	}

	remoteEntries := response.GetEntries()
	fuseEntries := []fuse.DirEntry{}

	for _, remoteEntry := range remoteEntries {
		fuseEntries = append(fuseEntries, fuse.DirEntry{
			Ino:  remoteEntry.Ino,
			Name: remoteEntry.Path,
			Mode: remoteEntry.Mode,
		})
	}

	return fuseEntries, nil
}

func downloadAndSave(remote *proto.DirEntry) error {
	// log.Printf("[INFO] Downloading remote file \"%v\"\n", remotePath)

	fullpath := filepath.Join(mountpoint, remote.Path)
	file, err := os.OpenFile(fullpath, os.O_RDWR, os.FileMode(remote.Mode))
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
	ctx := NewAuthenticatedCtx(context.Background())
	stream, err := grpcClient.DownloadFile(
		ctx,
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
	// log.Printf("[DEBUG] Received %v bytes of data from network\n", recvBytes)

	if totalExpectedSize != -1 && recvBytes != totalExpectedSize {
		return fmt.Errorf("expected file of size %v but got %v bytes instead", totalExpectedSize, recvBytes)
	}

	// log.Printf("[INFO] Download file \"%v\" successfull\n", remotePath)
	return nil
}
