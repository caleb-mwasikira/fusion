package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
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
	n, err := fh.file.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return nil, fs.ToErrno(err)
	}
	return fuse.ReadResultData(buf[:n]), fs.OK
}

func (fh *FileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n, err := fh.file.WriteAt(data, off)
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
	n, err := syscall.Seek(int(fd), int64(off), int(whence))
	return uint64(n), fs.ToErrno(err)
}

func downloadRemoteFile(ctx context.Context, remote *proto.Dirent) error {
	path := filepath.Join(rootDir, remote.Path)
	// log.Printf("Downloading remote file %v\n", path)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(remote.Mode))
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
	localFileHash := fmt.Sprintf("%x", md5.Sum(nil))

	// Download file
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

	if totalExpectedSize != -1 && recvBytes != totalExpectedSize {
		return fmt.Errorf("expected file of size %v but got %v bytes instead", totalExpectedSize, recvBytes)
	}
	return nil
}
