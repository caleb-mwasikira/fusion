package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
)

var (
	nodeIndex uint64 = 0
	ownerUid  uint32 = uint32(os.Getuid())
	ownerGid  uint32 = uint32(os.Getegid())
)

type FS struct{}

func (FS) Root() (fs.Node, error) {
	dir, errno := NewNode("/", os.ModeDir|0o755)
	if errno != 0 {
		return nil, syscall.EINVAL
	}
	return dir, nil
}

type Node struct {
	name     string
	attr     fuse.Attr
	data     []byte           // Used if node is a file
	children map[string]*Node // Used if node is a directory
}

func NewNode(name string, mode os.FileMode) (*Node, syscall.Errno) {
	if strings.TrimSpace(name) == "" {
		return nil, syscall.EINVAL
	}

	nodeIndex += 1
	now := time.Now()
	return &Node{
		name: name,
		attr: fuse.Attr{
			Inode: nodeIndex,
			Mode:  mode,
			Atime: now,
			Mtime: now,
			Ctime: now,
			Uid:   ownerUid,
			Gid:   ownerGid,
		},
		data:     []byte{},
		children: make(map[string]*Node),
	}, 0
}

func (n *Node) isDir() bool {
	return (n.attr.Mode & os.ModeDir) != 0
}

func (n *Node) entryExists(name string) bool {
	for _, child := range n.children {
		if child.name == name {
			return true
		}
	}
	return false
}

func (n *Node) Attr(ctx context.Context, attr *fuse.Attr) error {
	// log.Printf("Attr [%v]\n", n.name)
	*attr = n.attr
	return nil
}

func (n *Node) Lookup(ctx context.Context, name string) (fs.Node, error) {
	// log.Printf("Lookup %v in %v\n", name, n.name)
	for _, child := range n.children {
		if child.name == name {
			return child, nil
		}
	}
	return nil, syscall.ENOENT
}

func (n *Node) ReadDirAll(ctx context.Context) ([]fuse.Dirent, error) {
	// log.Printf("ReadDirAll in %v\n", n.name)
	dirEntries := []fuse.Dirent{}

	isRootDir := n.attr.Inode == 1
	if isRootDir {
		dirEntries = append(dirEntries, fuse.Dirent{
			Inode: 2,
			Name:  "hello.txt",
			Type:  fuse.DT_File,
		})
	}

	for _, child := range n.children {
		dirEntries = append(dirEntries, fuse.Dirent{
			Inode: child.attr.Inode,
			Type:  fuse.DT_Dir,
			Name:  child.name,
		})
	}
	return dirEntries, nil
}

func (n *Node) Mkdir(ctx context.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	log.Printf("Mkdir %v, mode = %v in %v\n", req.Name, req.Mode, n.name)
	if !n.isDir() {
		return nil, syscall.ENOTDIR
	}

	if n.entryExists(req.Name) {
		return nil, syscall.EEXIST
	}

	node, errno := NewNode(req.Name, req.Mode)
	if errno != 0 {
		return nil, errno
	}
	n.children[req.Name] = node
	return node, nil
}

func (n *Node) Getattr(ctx context.Context, req *fuse.GetattrRequest, resp *fuse.GetattrResponse) error {
	// log.Printf("Getattr of node %v\n", n.name)
	resp.Attr = n.attr
	return nil
}

// Designed for editing without which vi or emacs fail; Doesn't have to do anything
func (n *Node) Fsync(ctx context.Context, req *fuse.FsyncRequest) error {
	return nil
}

func (n *Node) Create(ctx context.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	log.Printf("Create %v, mode=%v in node %v\n", req.Name, req.Mode, n.name)
	if n.entryExists(req.Name) {
		return nil, nil, syscall.EEXIST
	}

	node, errno := NewNode(req.Name, req.Mode)
	if errno != 0 {
		return nil, nil, errno
	}
	n.children[req.Name] = node
	return node, node, nil
}

func (n *Node) ReadAll(ctx context.Context) ([]byte, error) {
	log.Printf("ReadAll data from node %v\n", n.name)
	if n.isDir() {
		return nil, syscall.EISDIR
	}
	return n.data, nil
}

func (n *Node) Write(ctx context.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	log.Printf("Write %v bytes of data, offset=%v to [%v]\n", len(req.Data), req.Offset, n.name)
	if n.isDir() {
		return syscall.EISDIR
	}

	newEnd := req.Offset + int64(len(req.Data))
	newData := make([]byte, newEnd)
	copy(newData[:req.Offset], n.data[:req.Offset])
	copy(newData[req.Offset:], req.Data[:])

	n.data = newData
	n.attr.Size = uint64(len(newData))
	n.attr.Mtime = time.Now()

	resp.Size = len(req.Data)

	return nil
}

func (n *Node) Rename(ctx context.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	log.Printf("Rename %v to %v in node %v\n", req.OldName, req.NewName, n.name)
	oldNode, ok := n.children[req.OldName]
	if !ok {
		return syscall.ENOENT
	}

	newNode, ok := newDir.(*Node)
	if !ok {
		return syscall.EIO
	}

	newNode.children = oldNode.children
	n.name = req.NewName
	delete(n.children, req.OldName)
	return nil
}

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("error getting user's home dir; %v\n", err)
	}

	fileSystem := "MEMORY_FS"
	mountPoint := filepath.Join(homeDir, fileSystem)

	log.Printf("mounting filesystem %v onto %v\n", fileSystem, mountPoint)
	conn, err := fuse.Mount(mountPoint, fuse.FSName(fileSystem))
	if err != nil {
		log.Fatalf("error mounting filesystem; %v\n", err)
	}
	defer conn.Close()

	// Unmount filesystem when server is shutting down
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signalChan
		log.Printf("Received signal; %v\n", sig)

		err := fuse.Unmount(mountPoint)
		if err != nil {
			log.Printf("Error unmounting filesystem %v; %v\n", fileSystem, err)
		}

		log.Println("Shutting down the server gracefully")
		os.Exit(1)
	}()

	err = fs.Serve(conn, FS{})
	if err != nil {
		log.Fatalf("error starting FUSE server; %v\n", err)
	}

}
