package shared

import (
	"log"
	"os"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func ReadDir(path string) (fs.DirStream, error) {
	entries := []fuse.DirEntry{}

	files, err := os.ReadDir(path)
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
	return fs.NewListDirStream(entries), nil
}

func CheckPermissions(path string) (bool, bool, bool) {
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("Error checking file stat; %v\n", err)
		return false, false, false
	}

	stat := info.Sys().(*syscall.Stat_t)
	mode := info.Mode().Perm()

	// Get current process UID/GID
	uid := os.Geteuid()
	gid := os.Getegid()

	var canRead, canWrite, canExec bool

	switch {
	case stat.Uid == uint32(uid):
		canRead = mode&0400 != 0
		canWrite = mode&0200 != 0
		canExec = mode&0100 != 0
	case stat.Gid == uint32(gid):
		canRead = mode&0040 != 0
		canWrite = mode&0020 != 0
		canExec = mode&0010 != 0
	default:
		canRead = mode&0004 != 0
		canWrite = mode&0002 != 0
		canExec = mode&0001 != 0
	}

	return canRead, canWrite, canExec
}
