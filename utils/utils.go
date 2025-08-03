package utils

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ProjectDir, CertDir string
)

func init() {
	_, file, _, _ := runtime.Caller(0)
	utilsDir := filepath.Dir(file)
	ProjectDir = filepath.Dir(utilsDir)
	CertDir = filepath.Join(ProjectDir, "certs")
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

func LoadEnvFile(path string) error {
	log.Println("Loading .env file")

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid .env file format")
		}

		key := parts[0]
		value := parts[1]
		value = strings.Trim(value, "\"")

		err := os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
}
