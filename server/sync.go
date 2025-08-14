package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/caleb-mwasikira/fusion/lib/events"
	"github.com/caleb-mwasikira/fusion/lib/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	// List of clients listening for changes on a directory
	observers = make(map[string][]chan *proto.FileEvent)
	broadcast = make(chan *proto.FileEvent, 100)
	mu        = sync.RWMutex{}
)

// Get all observers for provided path.
// Path doesn't have to be an exact match;
//
//	eg. An observer could be listening for changes on the path
//	/home/Documents but a file in /home/Documents/folder changes.
//	That observer should be notified of these changes.
func getObservers(path string) []chan *proto.FileEvent {
	mu.RLock()
	defer mu.RUnlock()

	clients := []chan *proto.FileEvent{}
	for observedPath, _observers := range observers {
		if strings.Contains(path, observedPath) {
			clients = append(clients, _observers...)
		}
	}
	return clients
}

// Function that listens for messages on the broadcast channel
// and forwards them to the observers.
// Should be run as a goroutine
func startMainObserver(ctx context.Context) {
	log.Println("Launching MAIN_OBSERVER goroutine")

	for {
		fileEvent := <-broadcast

		log.Printf("MAIN_OBSERVER received file event %v\n", fileEvent)

		clients := getObservers(fileEvent.Path)
		if len(clients) == 0 {
			log.Println("No clients observing file events from MAIN_OBSERVER")
			continue
		}

		for _, client := range clients {
			select {
			case <-ctx.Done():
				log.Printf("Exiting MAIN_OBSERVER goroutine; %v\n", ctx.Err())
				return

			default:
				go func(fileEvent *proto.FileEvent) {
					client <- fileEvent
				}(fileEvent)
			}
		}
	}
}

// Sends a message on the broadcast channel to notify observers
// of a file change
// Should be called as a goroutine
func notifyObservers(event events.EventType, path string, newpath string, mode os.FileMode) {
	path = relativePath(path)
	newpath = relativePath(newpath)

	fileEvent := &proto.FileEvent{
		Event:     uint32(event),
		Path:      path,
		NewPath:   newpath,
		Mode:      uint32(mode),
		Timestamp: timestamppb.Now(),
	}

	log.Printf("Broadcast file event %v -> MAIN_OBSERVER\n", fileEvent)
	broadcast <- fileEvent
}
