package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Node is a filesystem node in a loopback file system.
type Node struct {
	fs.Inode

	path string
}

var _ = (fs.NodeLookuper)((*Node)(nil))
var _ = (fs.NodeMkdirer)((*Node)(nil))
var _ = (fs.NodeRmdirer)((*Node)(nil))
var _ = (fs.NodeUnlinker)((*Node)(nil))
var _ = (fs.NodeRenamer)((*Node)(nil))
var _ = (fs.NodeCreater)((*Node)(nil))
var _ = (fs.NodeSymlinker)((*Node)(nil))
var _ = (fs.NodeLinker)((*Node)(nil))
var _ = (fs.NodeReadlinker)((*Node)(nil))
var _ = (fs.NodeOpener)((*Node)(nil))

// var _ = (fs.NodeOpendirHandler)((*Node)(nil))
var _ = (fs.NodeReaddirer)((*Node)(nil))
var _ = (fs.NodeGetattrer)((*Node)(nil))
var _ = (fs.NodeSetattrer)((*Node)(nil))
var _ = (fs.NodeOnForgetter)((*Node)(nil))

// NewFileSystem returns a root node for a loopback file system.
// This node implements all NodeXxxxer operations available.
func NewFileSystem(ctx context.Context, realpath string) (fs.InodeEmbedder, error) {
	// Confirm path exists
	var stat syscall.Stat_t
	err := syscall.Stat(realpath, &stat)
	if err != nil {
		return nil, err
	}

	go startRemoteObserver(ctx)

	return &Node{path: realpath}, nil
}

func relativePath(path string) string {
	return strings.TrimPrefix(path, realpath)
}

func (n *Node) OnAdd(ctx context.Context) {
	if !n.IsDir() {
		return
	}

	// log.Printf("[FUSE] OnAdd %v\n", n.path)

	relativePath := relativePath(n.path)
	err := fetchRemoteEntries(ctx, relativePath)
	if err != nil {
		log.Printf("[FUSE] Error fetching remote entries; %v\n", err)
		return
	}
}

func (n *Node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	log.Printf("[FUSE] Statfs %v\n", n.path)
	stat := syscall.Statfs_t{}
	err := syscall.Statfs(n.path, &stat)
	if err != nil {
		log.Printf("[FUSE] Stafs %v failed; %v\n", n.path, err)
		return fs.ToErrno(err)
	}
	out.FromStatfsT(&stat)
	return fs.OK
}

func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	// log.Printf("[FUSE] Lookup %v\n", fullpath)

	stat := syscall.Stat_t{}
	err := syscall.Lstat(fullpath, &stat)
	if err != nil {
		log.Printf("[FUSE] Lookup %v failed; %v\n", relativePath(fullpath), err)
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&stat)

	child := n.NewPersistentInode(
		ctx,
		&Node{path: fullpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)
	return child, 0
}

func (n *Node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Mkdir; %v\n", fullpath)

	// Create local directory
	err := os.MkdirAll(fullpath, os.FileMode(mode))
	if err != nil {
		log.Printf("[FUSE] Mkdir %v failed; %v\n", fullpath, err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		syscall.Rmdir(fullpath)
		log.Printf("[FUSE] Mkdir %v failed; %v\n", fullpath, err)
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&stat)

	child := n.NewPersistentInode(
		ctx,
		&Node{path: fullpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)

	// Create remote directory
	relativePath := relativePath(fullpath)

	go func(path string, mode uint32) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Mkdir(ctx, &proto.MkdirRequest{
			Path: path,
			Mode: mode,
		})
		if err != nil {
			log.Printf("[FUSE] Error creating remote directory; %v\n", err)
		}
	}(relativePath, stat.Mode)

	return child, 0
}

func (n *Node) Rmdir(ctx context.Context, name string) syscall.Errno {
	fullpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Rmdir %v\n", fullpath)

	err := syscall.Rmdir(fullpath)
	if err != nil {
		return fs.ToErrno(err)
	}

	// Remove remote directory
	relativePath := relativePath(fullpath)

	go func(path string) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Rmdir(ctx, &proto.DirEntry{
			Path: path,
		})
		if err != nil {
			log.Printf("[FUSE] Error deleting remote directory; %v\n", err)
		}
	}(relativePath)

	return fs.OK
}

func (n *Node) Unlink(ctx context.Context, name string) syscall.Errno {
	fullpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Unlink %v\n", fullpath)

	// Remove local file
	err := os.Remove(fullpath)
	if err != nil {
		return fs.ToErrno(err)
	}

	// Remove remote file
	relativePath := relativePath(fullpath)

	go func(path string) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Rmdir(ctx, &proto.DirEntry{
			Path: path,
		})
		if err != nil {
			log.Printf("[FUSE] Error deleting remote file; %v\n", err)
		}
	}(relativePath)

	return fs.ToErrno(err)
}

func (n *Node) Rename(ctx context.Context, oldName string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	newNode, ok := newParent.(*Node)
	if !ok {
		// log.Println("Rename failed; newNode is NOT of type *Node")
		return syscall.EXDEV
	}

	oldpath := filepath.Join(n.path, oldName)
	newpath := filepath.Join(newNode.path, newName)
	log.Printf("[FUSE] Rename %v -> %v\n", oldpath, newpath)

	newParentDir := filepath.Dir(newpath)
	if _, err := os.Stat(newParentDir); os.IsNotExist(err) {
		log.Printf("[FUSE] Target directory '%s' does not exist. Creating it.\n", newParentDir)
		err := os.MkdirAll(newParentDir, 0755)
		if err != nil {
			log.Printf("[FUSE] Failed to create target directory: %v\n", err)
			return fs.ToErrno(err)
		}
	}

	err := os.Rename(oldpath, newpath)
	if err != nil {
		log.Printf("[FUSE] Rename %v -> %v failed; %v\n", oldpath, newpath, err)
		return fs.ToErrno(err)
	}

	// Remove old entry from parent
	oldChild := n.GetChild(oldName)
	if oldChild != nil {
		n.MvChild(oldName, &newNode.Inode, newName, false)
		go func() {
			n.NotifyDelete(oldName, oldChild)

			// Notify kernel of new node
			newNode.NotifyEntry(newName)
		}()
	}

	var stat syscall.Stat_t
	err = syscall.Lstat(newpath, &stat)
	if err != nil {
		log.Printf("[FUSE] Rename %v -> %v failed; %v\n", oldpath, newpath, err)
		return fs.ToErrno(err)
	}

	child := newNode.NewPersistentInode(
		ctx,
		&Node{path: newpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	newNode.AddChild(newName, child, false)

	// Rename remote file
	oldpath = relativePath(oldpath)
	newpath = relativePath(newpath)

	go func(oldpath, newpath string) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Rename(ctx, &proto.RenameRequest{
			OldPath: oldpath,
			NewPath: newpath,
		})
		if err != nil {
			log.Printf("[FUSE] Error renaming remote file; %v\n", err)
		}
	}(oldpath, newpath)

	return 0
}

func (n *Node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Create %v\n", fullpath)

	file, err := os.OpenFile(fullpath, int(flags), os.FileMode(mode))
	if err != nil {
		log.Printf("[FUSE] Create %v failed; %v\n", fullpath, err)
		return nil, nil, 0, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Fstat(int(file.Fd()), &stat)
	if err != nil {
		log.Printf("[FUSE] Create %v failed; %v\n", fullpath, err)
		return nil, nil, 0, fs.ToErrno(err)
	}
	out.FromStat(&stat)

	child := n.NewPersistentInode(
		ctx,
		&Node{path: fullpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)

	// Create remote file
	relativePath := relativePath(fullpath)

	go func(path string, flags uint32, mode uint32) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Create(ctx, &proto.CreateRequest{
			Path:  path,
			Flags: flags,
			Mode:  mode,
		})
		if err != nil {
			log.Printf("[FUSE] Error creating remote file; %v\n", err)
		}
	}(relativePath, flags, mode)

	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	return child, NewLoopbackFile(fd, fullpath), 0, 0
}

func (n *Node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Symlink; %v\n", fullpath)

	err := syscall.Symlink(target, fullpath)
	if err != nil {
		log.Printf("[FUSE] Symlink %v failed; %v\n", fullpath, err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		syscall.Unlink(fullpath)
		log.Printf("[FUSE] Symlink %v failed; %v\n", fullpath, err)
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&stat)

	child := n.NewPersistentInode(
		ctx,
		&Node{path: fullpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)
	return child, 0
}

func (n *Node) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	targetNode, ok := target.(*Node)
	if !ok {
		// log.Println("Link failed; targetNode is NOT of type *Node")
		return nil, syscall.EIO
	}

	oldpath := filepath.Join(n.path, targetNode.path)
	newpath := filepath.Join(n.path, name)
	log.Printf("[FUSE] Link %v -> %v\n", oldpath, newpath)

	err := syscall.Link(oldpath, newpath)
	if err != nil {
		log.Printf("[FUSE] Link %v -> %v failed; %v\n", oldpath, newpath, err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(oldpath, &stat)
	if err != nil {
		syscall.Unlink(oldpath)
		log.Printf("[FUSE] Link %v -> %v failed; %v\n", oldpath, newpath, err)
		return nil, fs.ToErrno(err)
	}
	out.Attr.FromStat(&stat)

	child := n.NewPersistentInode(
		ctx,
		&Node{path: newpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)
	return child, 0
}

func (n *Node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	path := n.path

	// No idea whats going on here
	for l := 256; ; l *= 2 {
		buf := make([]byte, l)
		size, err := syscall.Readlink(path, buf)
		if err != nil {
			return nil, fs.ToErrno(err)
		}

		if size < len(buf) {
			return buf[:size], 0
		}
	}
}

func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	fullpath := n.path
	log.Printf("[FUSE] Open %v\n", fullpath)

	file, err := os.OpenFile(fullpath, int(flags), 0755)
	if err != nil {
		log.Printf("[FUSE] Open %v failed; %v\n", fullpath, err)
		return nil, 0, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		log.Printf("[FUSE] Open %v failed; %v\n", fullpath, err)
		return nil, 0, fs.ToErrno(err)
	}

	name := filepath.Base(fullpath)
	child := n.NewPersistentInode(
		ctx,
		&Node{path: fullpath},
		fs.StableAttr{
			Ino:  stat.Ino,
			Mode: stat.Mode,
		},
	)
	n.AddChild(name, child, false)

	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, 0, fs.ToErrno(err)
	}

	return NewLoopbackFile(fd, fullpath), 0, 0
}

func (n *Node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// log.Printf("[FUSE] Readdir %v\n", n.path)

	entries := []fuse.DirEntry{}
	files, err := os.ReadDir(n.path)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			continue
		}

		var mode uint32
		if info.IsDir() {
			mode = fuse.S_IFDIR | uint32(info.Mode().Perm())
		} else {
			mode = fuse.S_IFREG | uint32(info.Mode().Perm())
		}

		entries = append(entries, fuse.DirEntry{
			Name: f.Name(),
			Mode: mode,
			Ino:  uint64(info.Sys().(*syscall.Stat_t).Ino),
		})
	}
	return fs.NewListDirStream(entries), fs.OK
}

func (n *Node) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// log.Printf("[FUSE] Getattr %v\n", n.path)

	var err error
	st := syscall.Stat_t{}
	if &n.Inode == n.Root() {
		err = syscall.Stat(n.path, &st)
	} else {
		err = syscall.Lstat(n.path, &st)
	}

	if err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return fs.OK
}

func (n *Node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	fullpath := n.path
	log.Printf("[FUSE] Setattr %v\n", fullpath)
	mode, ok := in.GetMode()
	if ok {
		err := syscall.Chmod(fullpath, mode)
		if err != nil {
			log.Printf("[FUSE] Setattr %v failed; %v\n", fullpath, err)
			return fs.ToErrno(err)
		}
	}

	userId, uidOK := in.GetUID()
	groupId, gidOK := in.GetGID()
	if uidOK || gidOK {
		suid := -1
		sgid := -1

		if uidOK {
			suid = int(userId)
		}
		if gidOK {
			sgid = int(groupId)
		}

		err := syscall.Chown(fullpath, suid, sgid)
		if err != nil {
			log.Printf("[FUSE] Setattr %v failed; %v\n", n.path, err)
			return fs.ToErrno(err)
		}
	}

	// TODO: Implement setting modified and access times

	// modifiedTime, modifiedOK := in.GetMTime()
	// accessTime, accessOK := in.GetATime()

	// if modifiedOK || accessOK {

	// }

	size, ok := in.GetSize()
	if ok {
		err := syscall.Truncate(fullpath, int64(size))
		if err != nil {
			log.Printf("[FUSE] Setattr %v failed; %v\n", n.path, err)
			return fs.ToErrno(err)
		}
	}

	stat := syscall.Stat_t{}
	err := syscall.Lstat(fullpath, &stat)
	if err != nil {
		log.Printf("[FUSE] Setattr %v failed; %v\n", n.path, err)
		return fs.ToErrno(err)
	}
	out.FromStat(&stat)
	return fs.OK
}

func (n *Node) OnForget() {
	n.ForgetPersistent()
}
