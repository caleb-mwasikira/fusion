package utils

import (
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	CertDir string
)

func init() {
	_, file, _, _ := runtime.Caller(0)
	utilsDir := filepath.Dir(file)
	projectDir := filepath.Dir(utilsDir)
	CertDir = filepath.Join(projectDir, "certs")
}

func ReadLocalDir(path string) ([]fuse.DirEntry, error) {
	entries := []fuse.DirEntry{}

	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
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
	return entries, nil
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

func FileInfoToFileAttr(info os.FileInfo) *proto.FileAttr {
	stat := info.Sys().(*syscall.Stat_t)
	return StatToFileAttr(stat)
}

func StatToFileAttr(stat *syscall.Stat_t) *proto.FileAttr {
	accessTime := time.Unix(0, stat.Atim.Nsec)
	modifiedTime := time.Unix(0, stat.Mtim.Nsec)
	changeTime := time.Unix(0, stat.Ctim.Nsec)

	return &proto.FileAttr{
		Inode: stat.Ino,
		Size:  uint64(stat.Size),
		ATime: timestamppb.New(accessTime),
		MTime: timestamppb.New(modifiedTime),
		CTime: timestamppb.New(changeTime),
		Mode:  stat.Mode,
		NLink: uint32(stat.Nlink),
		Owner: &proto.Owner{
			Uid: stat.Uid,
			Gid: stat.Gid,
		},
		BlockSize: uint32(stat.Blksize),
	}
}

func FileAttrToFuseAttr(attr *proto.FileAttr) fuse.Attr {
	return fuse.Attr{
		Ino:   attr.Inode,
		Size:  attr.Size,
		Atime: uint64(attr.ATime.Nanos),
		Mtime: uint64(attr.MTime.Nanos),
		Ctime: uint64(attr.CTime.Nanos),
		Mode:  attr.Mode,
		Nlink: attr.NLink,
		Owner: fuse.Owner{
			Uid: attr.Owner.Uid,
			Gid: attr.Owner.Gid,
		},
		Blksize: attr.BlockSize,
	}
}

func IsDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}
	digest := hash.Sum(nil)
	return fmt.Sprintf("%x", digest), nil
}
