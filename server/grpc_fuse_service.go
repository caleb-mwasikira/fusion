package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/caleb-mwasikira/fusion/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type GrpcFuseService struct {
	proto.UnimplementedFuseServiceServer

	path string // Real path to where files are stored
}

var _ = (proto.FuseServiceServer)((*GrpcFuseService)(nil))

func (s GrpcFuseService) Attr(ctx context.Context, req *proto.Node) (*proto.FileAttr, error) {
	path := filepath.Join(s.path, req.Path)
	// log.Printf("GRPC Attr \"%v\"\n", path)

	stat := syscall.Stat_t{}
	err := syscall.Lstat(path, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return utils.StatToFileAttr(&stat), nil
}

func (s GrpcFuseService) Lookup(ctx context.Context, req *proto.LookupRequest) (*proto.Node, error) {
	path := filepath.Join(s.path, req.Path)
	log.Printf("GRPC Lookup \"%v\"\n", path)

	info, err := os.Stat(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	attr := utils.FileInfoToFileAttr(info)
	return &proto.Node{
		Path: req.Path,
		Attr: attr,
	}, nil
}

func (s GrpcFuseService) ReadDirAll(ctx context.Context, req *proto.Node) (*proto.ReadDirAllResponse, error) {
	dirPath := filepath.Join(s.path, req.Path)
	log.Printf("GRPC ReadDirAll \"%v\"\n", dirPath)

	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	entries := []*proto.Dirent{}
	for _, file := range files {
		filePath := filepath.Join(req.Path, file.Name())

		info, err := file.Info()
		if err != nil {
			continue
		}

		attr := utils.FileInfoToFileAttr(info)
		entries = append(entries, &proto.Dirent{
			Inode: attr.Inode,
			Path:  filePath,
			Mode:  uint32(info.Mode()),
		})
	}
	return &proto.ReadDirAllResponse{
		Entries: entries,
	}, nil
}

func (s GrpcFuseService) Mkdir(ctx context.Context, req *proto.MkdirRequest) (*proto.Node, error) {
	path := filepath.Join(s.path, req.Path)
	log.Printf("GRPC Mkdir \"%v\"\n", path)

	err := os.Mkdir(path, os.FileMode(req.Mode))
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	stat := syscall.Stat_t{}
	err = syscall.Lstat(path, &stat)
	if err != nil {
		os.Remove(path)
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &proto.Node{
		Path: req.Path,
		Attr: utils.StatToFileAttr(&stat),
	}, nil
}

func (s GrpcFuseService) Rmdir(ctx context.Context, req *proto.Node) (*emptypb.Empty, error) {
	path := filepath.Join(s.path, req.Path)

	err := os.Remove(path)
	if err != nil {
		code := codes.Internal
		if os.IsNotExist(err) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code, err.Error())
	}
	return &emptypb.Empty{}, nil
}

func (s GrpcFuseService) Getattr(ctx context.Context, req *proto.Node) (*proto.FileAttr, error) {
	path := filepath.Join(s.path, req.Path)
	// log.Printf("GRPC Getattr \"%v\"\n", path)

	stat := syscall.Stat_t{}
	err := syscall.Lstat(path, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return utils.StatToFileAttr(&stat), nil
}

func (s GrpcFuseService) Create(ctx context.Context, req *proto.CreateRequest) (*proto.CreateResponse, error) {
	path := filepath.Join(s.path, req.Path)
	log.Printf("GRPC Create \"%v\"\n", path)

	file, err := os.OpenFile(path, int(req.Flags), os.FileMode(req.Mode))
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	attr := utils.FileInfoToFileAttr(info)
	return &proto.CreateResponse{
		NodeId: attr.Inode,
		Attr:   attr,
	}, nil
}

func (s GrpcFuseService) Symlink(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	oldpath := filepath.Join(s.path, req.OldPath)
	newpath := filepath.Join(s.path, req.NewPath)
	log.Printf("GRPC Symlink %v -> %v\n", oldpath, newpath)

	err := syscall.Symlink(oldpath, newpath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	// Stat new path
	stat := syscall.Stat_t{}
	err = syscall.Lstat(newpath, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	return &proto.LinkResponse{
		Node: &proto.Node{
			Path: req.NewPath,
			Attr: utils.StatToFileAttr(&stat),
		},
	}, nil
}

func (s GrpcFuseService) Link(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	oldpath := filepath.Join(s.path, req.OldPath)
	newpath := filepath.Join(s.path, req.NewPath)
	log.Printf("GRPC Link %v -> %v\n", oldpath, newpath)

	err := syscall.Link(oldpath, newpath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	// Stat new path
	stat := syscall.Stat_t{}
	err = syscall.Stat(newpath, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	return &proto.LinkResponse{
		Node: &proto.Node{
			Path: req.NewPath,
			Attr: utils.StatToFileAttr(&stat),
		},
	}, nil
}

func (s GrpcFuseService) ReadAll(ctx context.Context, req *proto.Node) (*proto.ReadAllResponse, error) {
	path := filepath.Join(s.path, req.Path)
	log.Printf("GRPC ReadAll %v\n", path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &proto.ReadAllResponse{Data: data}, nil
}

func (GrpcFuseService) Write(context.Context, *proto.WriteRequest) (*proto.WriteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Write not implemented")
}

func (s GrpcFuseService) Rename(ctx context.Context, req *proto.RenameRequest) (*emptypb.Empty, error) {
	oldpath := filepath.Join(s.path, req.OldPath)
	newpath := filepath.Join(s.path, req.NewPath)
	log.Printf("GRPC Rename %v -> %v\n", oldpath, newpath)

	err := syscall.Rename(oldpath, newpath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &emptypb.Empty{}, nil
}

func (s GrpcFuseService) DownloadFile(req *proto.DownloadRequest, stream grpc.ServerStreamingServer[proto.FileChunk]) error {
	log.Printf("GRPC DownloadFile %v\n", req.Path)

	path := filepath.Join(s.path, req.Path)
	stat := syscall.Stat_t{}
	err := syscall.Stat(path, &stat)
	if err != nil {
		errCode := codes.Internal
		if err == syscall.ENOENT {
			errCode = codes.NotFound
		}
		return status.Error(errCode, err.Error())
	}

	mode := os.FileMode(stat.Mode)
	if mode.IsDir() {
		// Cannot download a directory
		return status.Error(codes.InvalidArgument, "path is a directory")
	}

	fileHash, err := utils.HashFile(path)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	if fileHash == req.ExpectedHash {
		// Remote and local files match,
		// no need to send file data
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	defer file.Close()

	buff := make([]byte, 64*1024) // 64Kb
	sentBytes := 0

	for {
		n, err := file.Read(buff)
		if err != nil {
			if err == io.EOF {
				break
			}
			return status.Error(codes.Internal, err.Error())
		}

		chunk := proto.FileChunk{
			Data:      buff[:n],
			Offset:    int64(sentBytes),
			TotalSize: stat.Size,
		}
		err = stream.Send(&chunk)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}

		sentBytes += n
	}
	return nil
}
