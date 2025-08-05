// Copyright 2019 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"log"
	"sync"
	"syscall"

	"github.com/caleb-mwasikira/fusion/proto"
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
var _ = (fs.FilePassthroughFder)((*FileHandle)(nil))

func (f *FileHandle) PassthroughFd() (int, bool) {
	// This Fd is not accessed concurrently, but lock anyway for uniformity.
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fd, true
}

func (f *FileHandle) Read(ctx context.Context, buf []byte, off int64) (res fuse.ReadResult, errno syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := fuse.ReadResultFd(uintptr(f.fd), off, len(buf))
	return r, fs.OK
}

func (f *FileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := syscall.Pwrite(f.fd, data, off)
	if err != nil {
		return 0, fs.ToErrno(err)
	}

	// Write remote file
	relativePath := relativePath(f.path)

	go func(path string) {
		ctx = NewAuthenticatedCtx(ctx)
		_, err := grpcClient.Write(ctx, &proto.WriteRequest{
			Path:   path,
			Offset: off,
			Data:   data,
		})
		if err != nil {
			log.Printf("[ERROR] writing to remote file; %v\n", err)
		}
	}(relativePath)

	return uint32(n), fs.ToErrno(err)
}

func (f *FileHandle) Release(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fd != -1 {
		err := syscall.Close(f.fd)
		f.fd = -1
		return fs.ToErrno(err)
	}
	return syscall.EBADF
}

func (f *FileHandle) Flush(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Since Flush() may be called for each dup'd fd, we don't
	// want to really close the file, we just want to flush. This
	// is achieved by closing a dup'd fd.
	newFd, err := syscall.Dup(f.fd)
	if err != nil {
		return fs.ToErrno(err)
	}
	err = syscall.Close(newFd)
	return fs.ToErrno(err)
}

func (f *FileHandle) Fsync(ctx context.Context, flags uint32) (errno syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := fs.ToErrno(syscall.Fsync(f.fd))

	return r
}

const (
	_OFD_GETLK  = 36
	_OFD_SETLK  = 37
	_OFD_SETLKW = 38
)

func (f *FileHandle) Getlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, out *fuse.FileLock) (errno syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)
	errno = fs.ToErrno(syscall.FcntlFlock(uintptr(f.fd), _OFD_GETLK, &flk))
	out.FromFlockT(&flk)
	return
}

func (f *FileHandle) Setlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) (errno syscall.Errno) {
	return f.setLock(ctx, owner, lk, flags, false)
}

func (f *FileHandle) Setlkw(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) (errno syscall.Errno) {
	return f.setLock(ctx, owner, lk, flags, true)
}

func (f *FileHandle) setLock(_ context.Context, _ uint64, lk *fuse.FileLock, flags uint32, blocking bool) (errno syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
		return fs.ToErrno(syscall.Flock(f.fd, op))
	} else {
		flk := syscall.Flock_t{}
		lk.ToFlockT(&flk)
		var op int
		if blocking {
			op = _OFD_SETLKW
		} else {
			op = _OFD_SETLK
		}
		return fs.ToErrno(syscall.FcntlFlock(uintptr(f.fd), op, &flk))
	}
}

func (f *FileHandle) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	mode, ok := in.GetMode()
	if ok {
		err := syscall.Fchmod(f.fd, mode)
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
		err := syscall.Fchown(f.fd, uid, gid)
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
		err := syscall.Ftruncate(f.fd, int64(size))
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	return f.Getattr(ctx, out)
}

func (f *FileHandle) Getattr(ctx context.Context, a *fuse.AttrOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := syscall.Stat_t{}
	err := syscall.Fstat(f.fd, &st)
	if err != nil {
		return fs.ToErrno(err)
	}
	a.FromStat(&st)

	return fs.OK
}

func (f *FileHandle) Lseek(ctx context.Context, off uint64, whence uint32) (uint64, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := unix.Seek(f.fd, int64(off), int(whence))
	return uint64(n), fs.ToErrno(err)
}

// func (f *FileHandle) Allocate(ctx context.Context, off uint64, size uint64, mode uint32) syscall.Errno {
// 	f.mu.Lock()
// 	defer f.mu.Unlock()
// 	err := fallocate.Fallocate(f.fd, mode, int64(off), int64(size))
// 	if err != nil {
// 		return fs.ToErrno(err)
// 	}
// 	return fs.OK
// }

// func (f *FileHandle) Ioctl(ctx context.Context, cmd uint32, arg uint64, input []byte, output []byte) (result int32, errno syscall.Errno) {
// 	f.mu.Lock()
// 	defer f.mu.Unlock()

// 	argWord := uintptr(arg)
// 	ioc := ioctl.Command(cmd)
// 	if ioc.Read() {
// 		argWord = uintptr(unsafe.Pointer(&input[0]))
// 	} else if ioc.Write() {
// 		argWord = uintptr(unsafe.Pointer(&output[0]))
// 	}

// 	res, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f.fd), uintptr(cmd), argWord)
// 	return int32(res), errno
// }
