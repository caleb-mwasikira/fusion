// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"log"
	"os"
	"sync"
	"syscall"

	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

type FileHandle struct {
	mu   sync.Mutex
	fd   int
	path string
}

// NewLoopbackFile creates a FileHandle out of a file descriptor. All
// operations are implemented. When using the Fd from a *os.File, call
// syscall.Dup() on the fd, to avoid os.File's finalizer from closing
// the file descriptor.
func NewLoopbackFile(fd int, path string) fs.FileHandle {
	return &FileHandle{
		fd:   fd,
		path: path,
	}
}

var _ = (fs.FileHandle)((*FileHandle)(nil))
var _ = (fs.FileReleaser)((*FileHandle)(nil))
var _ = (fs.FileGetattrer)((*FileHandle)(nil))
var _ = (fs.FileReader)((*FileHandle)(nil))
var _ = (fs.FileWriter)((*FileHandle)(nil))
var _ = (fs.FileGetlker)((*FileHandle)(nil))
var _ = (fs.FileSetlker)((*FileHandle)(nil))
var _ = (fs.FileSetlkwer)((*FileHandle)(nil))
var _ = (fs.FileLseeker)((*FileHandle)(nil))
var _ = (fs.FileFlusher)((*FileHandle)(nil))
var _ = (fs.FileFsyncer)((*FileHandle)(nil))
var _ = (fs.FileSetattrer)((*FileHandle)(nil))

// var _ = (fs.FileAllocater)((*FileHandle)(nil))

func (fh *FileHandle) Read(ctx context.Context, buf []byte, off int64) (res fuse.ReadResult, errno syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	log.Printf("[FUSE] Read file %v\n", fh.path)

	// Before reading a file, we are going to download remote updates
	remote := proto.DirEntry{
		Path: relativePath(fh.path),
	}
	err := downloadFile(&remote)
	if err != nil {
		log.Printf("[SYNC] Error syncing file %v with remote; %v\n", fh.path, err)
	}

	r := fuse.ReadResultFd(uintptr(fh.fd), off, len(buf))
	return r, fs.OK
}

func (fh *FileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	log.Printf("[FUSE] Write file %v\n", fh.path)

	n, err := syscall.Pwrite(fh.fd, data, off)
	if err != nil {
		log.Printf("[FUSE] Error writing to file; %v\n", err)
		return 0, fs.ToErrno(err)
	}

	// Write remote file
	relativePath := relativePath(fh.path)

	go func(path string) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Write(ctx, &proto.WriteRequest{
			Path:   path,
			Offset: off,
			Data:   data,
		})
		if err != nil {
			log.Printf("[FUSE] Error writing to remote file; %v\n", err)
		}
	}(relativePath)

	return uint32(n), fs.ToErrno(err)
}

func (fh *FileHandle) Release(ctx context.Context) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	// Attempt to close the file descriptor.
	if fh.fd != -1 {
		syscall.Close(fh.fd)
		fh.fd = -1
	}
	// Always return OK.
	return fs.OK
}

func (fh *FileHandle) Flush(ctx context.Context) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	// Written files are never flushed.
	// This is bad. But so long as it saves me from debugging file
	// not found errors, I will keep it this way.
	return fs.OK
}

func (fh *FileHandle) Fsync(ctx context.Context, flags uint32) (errno syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	log.Printf("[FUSE] Fsync file %v\n", fh.path)

	// Check if the file still exists, and if so, perform the sync.
	if _, err := os.Stat(fh.path); err == nil {
		err = syscall.Fsync(fh.fd)
		if err != nil {
			return fs.ToErrno(err)
		}
	}
	return fs.OK
}

const (
	_OFD_GETLK  = 36
	_OFD_SETLK  = 37
	_OFD_SETLKW = 38
)

func (fh *FileHandle) Getlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, out *fuse.FileLock) (errno syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)
	errno = fs.ToErrno(syscall.FcntlFlock(uintptr(fh.fd), _OFD_GETLK, &flk))
	out.FromFlockT(&flk)
	return
}

func (fh *FileHandle) Setlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) (errno syscall.Errno) {
	return fh.setLock(ctx, owner, lk, flags, false)
}

func (fh *FileHandle) Setlkw(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) (errno syscall.Errno) {
	return fh.setLock(ctx, owner, lk, flags, true)
}

func (fh *FileHandle) setLock(_ context.Context, _ uint64, lk *fuse.FileLock, flags uint32, blocking bool) (errno syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	if (flags & fuse.FUSE_LK_FLOCK) != 0 {
		var op int
		switch lk.Typ {
		case syscall.F_RDLCK:
			op = syscall.LOCK_SH
		case syscall.F_WRLCK:
			op = syscall.LOCK_EX
		case syscall.F_UNLCK:
			op = syscall.LOCK_UN
		default:
			return syscall.EINVAL
		}
		if !blocking {
			op |= syscall.LOCK_NB
		}
		return fs.ToErrno(syscall.Flock(fh.fd, op))
	} else {
		flk := syscall.Flock_t{}
		lk.ToFlockT(&flk)
		var op int
		if blocking {
			op = _OFD_SETLKW
		} else {
			op = _OFD_SETLK
		}
		return fs.ToErrno(syscall.FcntlFlock(uintptr(fh.fd), op, &flk))
	}
}

func (fh *FileHandle) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	mode, ok := in.GetMode()
	if ok {
		err := syscall.Fchmod(fh.fd, mode)
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	uid32, uOk := in.GetUID()
	gid32, gOk := in.GetGID()
	if uOk || gOk {
		uid := -1
		gid := -1

		if uOk {
			uid = int(uid32)
		}
		if gOk {
			gid = int(gid32)
		}

		// Change owner
		err := syscall.Fchown(fh.fd, uid, gid)
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	// mtime, mok := in.GetMTime()
	// atime, aok := in.GetATime()

	// if mok || aok {
	// 	// Change modified and access times
	// }

	size, ok := in.GetSize()
	if ok {
		err := syscall.Ftruncate(fh.fd, int64(size))
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	return fh.Getattr(ctx, out)
}

func (fh *FileHandle) Getattr(ctx context.Context, a *fuse.AttrOut) syscall.Errno {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	st := syscall.Stat_t{}
	err := syscall.Fstat(fh.fd, &st)
	if err != nil {
		return fs.ToErrno(err)
	}
	a.FromStat(&st)

	return fs.OK
}

func (fh *FileHandle) Lseek(ctx context.Context, off uint64, whence uint32) (uint64, syscall.Errno) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	n, err := unix.Seek(fh.fd, int64(off), int(whence))
	return uint64(n), fs.ToErrno(err)
}

// func (fh *FileHandle) Allocate(ctx context.Context, off uint64, size uint64, mode uint32) syscall.Errno {
// 	fh.mu.Lock()
// 	defer fh.mu.Unlock()
// 	err := fallocate.Fallocate(fh.fd, mode, int64(off), int64(size))
// 	if err != nil {
// 		return fs.ToErrno(err)
// 	}
// 	return fs.OK
// }

// func (fh *FileHandle) Ioctl(ctx context.Context, cmd uint32, arg uint64, input []byte, output []byte) (result int32, errno syscall.Errno) {
// 	fh.mu.Lock()
// 	defer fh.mu.Unlock()

// 	argWord := uintptr(arg)
// 	ioc := ioctl.Command(cmd)
// 	if ioc.Read() {
// 		argWord = uintptr(unsafe.Pointer(&input[0]))
// 	} else if ioc.Write() {
// 		argWord = uintptr(unsafe.Pointer(&output[0]))
// 	}

// 	res, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fh.fd), uintptr(cmd), argWord)
// 	return int32(res), errno
// }
