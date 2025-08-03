package lib

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/hanwen/go-fuse/v2/fuse"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ProjectDir string
)

func init() {
	// Ensure project directory folder is created on
	// users home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Error getting users home directory; %v\n", err)
	}

	ProjectDir = filepath.Join(homeDir, ".fusion")
	err = os.MkdirAll(ProjectDir, 0755)
	if err != nil {
		log.Fatalf("Error creating project directory; %v\n", err)
	}
}

func LoadEnv() error {
	envFile := filepath.Join(ProjectDir, ".env")

	data, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.SplitN(line, "=", 2)
		if len(fields) != 2 {
			return fmt.Errorf("invalid .env file format near; %v", line)
		}

		key := strings.Trim(fields[0], "\"")
		value := strings.Trim(fields[1], "\"")
		err = os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
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
