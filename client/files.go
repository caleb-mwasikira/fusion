package main

import (
	"context"
	"os"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

type FileHandle struct {
	file *os.File
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

func (fh *FileHandle) Read(ctx context.Context, buf []byte, off int64) (res fuse.ReadResult, errno syscall.Errno) {
	fd := fh.file.Fd()
	n, err := syscall.Pread(int(fd), buf, off)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return fuse.ReadResultFd(uintptr(fd), off, n), fs.OK
}

func (fh *FileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	fd := fh.file.Fd()
	n, err := syscall.Pwrite(int(fd), data, off)
	return uint32(n), fs.ToErrno(err)
}

func (fh *FileHandle) Release(ctx context.Context) syscall.Errno {
	if fh.file != nil {
		fh.file.Close()
	}
	return 0
}

func (fh *FileHandle) Flush(ctx context.Context) syscall.Errno {
	fd := fh.file.Fd()
	newFd, err := syscall.Dup(int(fd))
	if err != nil {
		return fs.ToErrno(err)
	}

	err = syscall.Close(newFd)
	return fs.ToErrno(err)
}

func (fh *FileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	fd := fh.file.Fd()
	err := syscall.Fsync(int(fd))
	return fs.ToErrno(err)
}

const (
	GETLK  = 36
	SETLK  = 37
	SETLKW = 38
)

func (fh *FileHandle) Getlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, out *fuse.FileLock) syscall.Errno {
	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)

	fd := fh.file.Fd()
	err := syscall.FcntlFlock(uintptr(fd), GETLK, &flk)
	if err != nil {
		return fs.ToErrno(err)
	}

	out.FromFlockT(&flk)
	return fs.OK
}

func (fh *FileHandle) Setlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	return fh.setLock(ctx, owner, lk, flags, false)
}

func (fh *FileHandle) Setlkw(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	return fh.setLock(ctx, owner, lk, flags, true)
}

func (fh *FileHandle) setLock(_ context.Context, _ uint64, lk *fuse.FileLock, flags uint32, blocking bool) syscall.Errno {
	fd := fh.file.Fd()

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
		return fs.ToErrno(syscall.Flock(int(fd), op))
	} else {
		flk := syscall.Flock_t{}
		lk.ToFlockT(&flk)
		var op int
		if blocking {
			op = SETLKW
		} else {
			op = SETLK
		}
		return fs.ToErrno(syscall.FcntlFlock(uintptr(int(fd)), op, &flk))
	}
}

func (fh *FileHandle) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	fd := fh.file.Fd()

	mode, ok := in.GetMode()
	if ok {
		err := syscall.Fchmod(int(fd), mode)
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

		err := syscall.Fchown(int(fd), uid, gid)
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	size, ok := in.GetSize()
	if ok {
		err := syscall.Ftruncate(int(fd), int64(size))
		if err != nil {
			return fs.ToErrno(err)
		}
	}

	return fh.Getattr(ctx, out)
}

func (fh *FileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	fd := fh.file.Fd()
	stat := syscall.Stat_t{}
	err := syscall.Fstat(int(fd), &stat)
	if err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&stat)

	return fs.OK
}

func (fh *FileHandle) Lseek(ctx context.Context, off uint64, whence uint32) (uint64, syscall.Errno) {
	fd := fh.file.Fd()
	n, err := unix.Seek(int(fd), int64(off), int(whence))
	return uint64(n), fs.ToErrno(err)
}
