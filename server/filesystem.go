package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/caleb-mwasikira/fusion/lib/events"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// Get relative path for both FUSE filesystem and GRPC
// FUSE filesystem writes to realpath
// GRPC writes to the mountpoint
func relativePath(path string) string {
	path = strings.TrimPrefix(path, realpath)
	path = strings.TrimPrefix(path, mountpoint)
	return path
}

// Node is a filesystem node in a loopback file system.
type Node struct {
	fs.Inode

	path string
}

var _ = (fs.NodeOnAdder)((*Node)(nil))
var _ = (fs.NodeStatfser)((*Node)(nil))
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
var _ = (fs.NodeOpendirHandler)((*Node)(nil))
var _ = (fs.NodeReaddirer)((*Node)(nil))
var _ = (fs.NodeGetattrer)((*Node)(nil))
var _ = (fs.NodeSetattrer)((*Node)(nil))
var _ = (fs.NodeOnForgetter)((*Node)(nil))

// NewFileSystem returns a root node for a loopback file system.
// This node implements all NodeXxxxer operations available.
func NewFileSystem(realpath string) (fs.InodeEmbedder, error) {
	// Confirm path exists
	var stat syscall.Stat_t
	err := syscall.Stat(realpath, &stat)
	if err != nil {
		return nil, err
	}

	return &Node{path: realpath}, nil
}

func (n *Node) OnAdd(ctx context.Context) {
	if !n.IsDir() {
		return
	}

	files, err := os.ReadDir(n.path)
	if err != nil {
		return
	}

	for _, file := range files {
		fullpath := filepath.Join(n.path, file.Name())

		stat := syscall.Stat_t{}
		err = syscall.Lstat(fullpath, &stat)
		if err != nil {
			continue
		}

		child := n.NewPersistentInode(
			ctx,
			&Node{path: fullpath},
			fs.StableAttr{
				Ino:  stat.Ino,
				Mode: stat.Mode,
			},
		)
		n.AddChild(file.Name(), child, false)
	}
}

func (n *Node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	// log.Printf("Statfs %v\n", n.path)
	stat := syscall.Statfs_t{}
	err := syscall.Statfs(n.path, &stat)
	if err != nil {
		// log.Printf("Stafs %v failed; %v\n", n.path, err)
		return fs.ToErrno(err)
	}
	out.FromStatfsT(&stat)
	return fs.OK
}

func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	// log.Printf("Lookup %v\n", n.relativePath(fullpath))

	stat := syscall.Stat_t{}
	err := syscall.Lstat(fullpath, &stat)
	if err != nil {
		// log.Printf("Lookup %v failed; %v\n", n.relativePath(fullpath), err)
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
	return child, fs.OK
}

func (n *Node) Mkdir(ctx context.Context, name string, _mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	mode := os.FileMode(_mode) | os.ModeDir

	// log.Printf("Mkdir; %v\n", n.relativePath(fullpath))

	err := os.Mkdir(fullpath, mode)
	if err != nil {
		// log.Printf("Mkdir %v failed; %v\n", n.relativePath(fullpath), err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		syscall.Rmdir(fullpath)
		// log.Printf("Mkdir %v failed; %v\n", n.relativePath(fullpath), err)
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

	go notifyObservers(
		events.ADD_FILE, fullpath, "", mode,
	)

	return child, fs.OK
}

func (n *Node) Rmdir(ctx context.Context, name string) syscall.Errno {
	fullpath := filepath.Join(n.path, name)
	// log.Printf("Rmdir %v\n", n.relativePath(fullpath))

	err := syscall.Rmdir(fullpath)
	if err != nil {
		return fs.ToErrno(err)
	}

	go n.RmChild(name)

	go notifyObservers(
		events.DELETE_FILE, fullpath, "", 0,
	)

	return fs.OK
}

func (n *Node) Unlink(ctx context.Context, name string) syscall.Errno {
	fullpath := filepath.Join(n.path, name)
	// log.Printf("Unlink %v\n", n.relativePath(fullpath))
	err := syscall.Unlink(fullpath)
	if err != nil {
		return fs.ToErrno(err)
	}
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
	// log.Printf("Rename %v -> %v\n", oldpath, newpath)
	err := os.Rename(oldpath, newpath)
	if err != nil {
		// log.Printf("Rename %v -> %v failed; %v\n", oldpath, newpath, err)
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
		// log.Printf("Rename %v -> %v failed; %v\n", oldpath, newpath, err)
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

	// For rename, we send 2 file events; delete oldpath, and create newpath
	go notifyObservers(
		events.RENAME_FILE, oldpath, relativePath(newpath), 0,
	)

	return fs.OK
}

func (n *Node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	log.Printf("Create %v\n", relativePath(fullpath))

	file, err := os.OpenFile(fullpath, int(flags), 0755)
	if err != nil {
		// log.Printf("Create %v failed; %v\n", n.relativePath(fullpath), err)
		return nil, nil, 0, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Fstat(int(file.Fd()), &stat)
	if err != nil {
		// log.Printf("Create %v failed; %v\n", n.relativePath(fullpath), err)
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

	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	go notifyObservers(
		events.ADD_FILE, fullpath, "", os.FileMode(stat.Mode),
	)

	return child, NewLoopbackFile(fd, fullpath), 0, fs.OK
}

func (n *Node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	fullpath := filepath.Join(n.path, name)
	// log.Printf("Symlink; %v\n", n.relativePath(fullpath))

	err := syscall.Symlink(target, relativePath(fullpath))
	if err != nil {
		// log.Printf("Symlink %v failed; %v\n", n.relativePath(fullpath), err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		syscall.Unlink(fullpath)
		// log.Printf("Symlink %v failed; %v\n", n.relativePath(fullpath), err)
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
	return child, fs.OK
}

func (n *Node) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	targetNode, ok := target.(*Node)
	if !ok {
		// log.Println("Link failed; targetNode is NOT of type *Node")
		return nil, syscall.EIO
	}

	oldpath := filepath.Join(n.path, targetNode.path)
	newpath := filepath.Join(n.path, name)
	// log.Printf("Link %v -> %v\n", oldpath, newpath)
	err := syscall.Link(oldpath, newpath)
	if err != nil {
		// log.Printf("Link %v -> %v failed; %v\n", oldpath, newpath, err)
		return nil, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(oldpath, &stat)
	if err != nil {
		syscall.Unlink(oldpath)
		// log.Printf("Link %v -> %v failed; %v\n", oldpath, newpath, err)
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
	return child, fs.OK
}

func (n *Node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	fullpath := n.path

	// No idea whats going on here
	for l := 256; ; l *= 2 {
		buf := make([]byte, l)
		size, err := syscall.Readlink(fullpath, buf)
		if err != nil {
			return nil, fs.ToErrno(err)
		}

		if size < len(buf) {
			return buf[:size], 0
		}
	}
}

func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// log.Printf("Open %v\n", n.path)
	file, err := os.OpenFile(n.path, int(flags), 0755)
	if err != nil {
		// log.Printf("Open %v failed; %v\n", n.path, err)
		return nil, 0, fs.ToErrno(err)
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(n.path, &stat)
	if err != nil {
		// log.Printf("Open %v failed; %v\n", n.path, err)
		return nil, 0, fs.ToErrno(err)
	}

	name := filepath.Base(n.path)
	child := n.NewPersistentInode(
		ctx,
		&Node{path: n.path},
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

	return NewLoopbackFile(fd, n.path), 0, fs.OK
}

func (n *Node) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// log.Printf("OpendirHandle %v\n", n.path)

	ds, errno := fs.NewLoopbackDirStream(n.path)
	if errno != 0 {
		// log.Printf("OpendirHandle %v failed; %v\n", n.path, errno)
		return nil, 0, errno
	}
	return ds, 0, errno
}

func (n *Node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// log.Printf("Readdir %v\n", n.path)
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
	// log.Printf("Getattr %v\n", n.path)

	stat := syscall.Stat_t{}
	err := syscall.Lstat(n.path, &stat)
	if err != nil {
		// log.Printf("Getattr %v failed; %v\n", n.path, err)
		return fs.ToErrno(err)
	}
	out.Attr.FromStat(&stat)
	return fs.OK
}

func (n *Node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	fullpath := n.path
	// log.Printf("Setattr %v\n", n.path)
	mode, ok := in.GetMode()
	if ok {
		err := syscall.Chmod(fullpath, mode)
		if err != nil {
			// log.Printf("Setattr %v failed; %v\n", n.path, err)
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
			// log.Printf("Setattr %v failed; %v\n", n.path, err)
			return fs.ToErrno(err)
		}
	}

	modifiedTime, modifiedOK := in.GetMTime()
	accessTime, accessOK := in.GetATime()

	if modifiedOK || accessOK {
		accessTimestamp := unix.Timespec{}
		modifiedTimestamp := unix.Timespec{}

		var err error
		if accessOK {
			accessTimestamp, err = unix.TimeToTimespec(accessTime)
			if err != nil {
				// log.Printf("Setattr %v failed; %v\n", n.path, err)
				return fs.ToErrno(err)
			}
		}
		if modifiedOK {
			modifiedTimestamp, err = unix.TimeToTimespec(modifiedTime)
			if err != nil {
				// log.Printf("Setattr %v failed; %v\n", n.path, err)
				return fs.ToErrno(err)
			}
		}

		timestamp := []unix.Timespec{
			accessTimestamp,
			modifiedTimestamp,
		}
		err = unix.UtimesNanoAt(unix.AT_FDCWD, fullpath, timestamp, unix.AT_SYMLINK_NOFOLLOW)
		if err != nil {
			// log.Printf("Setattr %v failed; %v\n", n.path, err)
			return fs.ToErrno(err)
		}
	}

	size, ok := in.GetSize()
	if ok {
		err := syscall.Truncate(fullpath, int64(size))
		if err != nil {
			// log.Printf("Setattr %v failed; %v\n", n.path, err)
			return fs.ToErrno(err)
		}
	}

	stat := syscall.Stat_t{}
	err := syscall.Lstat(fullpath, &stat)
	if err != nil {
		// log.Printf("Setattr %v failed; %v\n", n.path, err)
		return fs.ToErrno(err)
	}
	out.FromStat(&stat)
	return fs.OK
}

func (n *Node) OnForget() {
	n.ForgetPersistent()
}
