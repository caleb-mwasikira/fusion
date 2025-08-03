package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/caleb-mwasikira/fusion/server/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type FuseServer struct {
	proto.UnimplementedFuseServer

	path string // Real path to where files are stored
}

var _ = (proto.FuseServer)((*FuseServer)(nil))

func (s FuseServer) relativePath(path string) string {
	return strings.TrimPrefix(path, s.path)
}

func (s FuseServer) Auth(ctx context.Context, req *proto.AuthRequest) (*proto.AuthResponse, error) {
	log.Printf("[GRPC] Auth %v\n", req.Username)

	user, ok := authUser(req.Username, req.Password)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "Invalid username or password")
	}

	tokenString, err := generateToken(*user)
	if err != nil {
		return nil, status.Error(codes.Internal, "[ERROR] generating json web token")
	}
	return &proto.AuthResponse{
		Token: tokenString,
	}, nil
}

// Gets the logged in user's root directory
//
//	returns:
//		string: path they are allowed access to
//		error: if access is denied
func (s FuseServer) getUsersBaseDir(ctx context.Context) (string, error) {
	user, ok := ctx.Value(userCtxKey).(*db.User)
	if !ok {
		// Usr is NOT logged in
		// The system should never reach this state as we are relying on the
		// auth interceptor to filter unauthenticated gRPC requests
		log.Println("[ERROR] User not logged in")
		return "", status.Error(codes.FailedPrecondition, "User not logged in")
	}

	baseDir := filepath.Join(s.path, user.OrgName, user.DeptName)

	// Check if directory exists
	stat := syscall.Stat_t{}
	err := syscall.Stat(baseDir, &stat)
	if err != nil {
		relativePath := s.relativePath(baseDir)
		log.Printf("[ERROR] User's base directory \"%v\" NOT found\n", relativePath)
		return "", status.Errorf(codes.NotFound, "User's base directory \"%v\" NOT found", relativePath)
	}

	return baseDir, nil
}

func (s FuseServer) CreateOrg(ctx context.Context, req *proto.CreateOrgRequest) (*emptypb.Empty, error) {
	log.Printf("[GRPC] CreateOrg %v[%v]\n", req.OrgName, req.DeptName)
	isEmpty := func(value string) bool {
		return strings.TrimSpace(value) == ""
	}

	if isEmpty(req.OrgName) {
		return nil, status.Error(codes.InvalidArgument, "Missing argument OrgName")
	}

	// Create organization directory
	baseDir := filepath.Join(s.path, req.OrgName)
	err := os.MkdirAll(baseDir, 0751)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if !isEmpty(req.DeptName) {
		// Create department directory
		deptDir := filepath.Join(baseDir, req.DeptName)
		err := os.MkdirAll(deptDir, 0771)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &emptypb.Empty{}, nil
}

func (s FuseServer) CreateUser(ctx context.Context, req *proto.CreateUserRequest) (*emptypb.Empty, error) {
	log.Printf("[GRPC] CreateUser %v@%v[%v]\n", req.Username, req.OrgName, req.DeptName)

	// Verify that users orgName and deptName exist
	baseDir := filepath.Join(s.path, req.OrgName, req.DeptName)
	if !dirExists(baseDir) {
		return nil, status.Errorf(codes.NotFound, "Organization \"%v\" with department \"%v\" NOT found", req.OrgName, req.DeptName)
	}

	user, err := db.NewUser(
		req.Username, req.Password,
		req.OrgName, req.DeptName,
		SECRET_KEY,
	)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	_, err = userModel.Insert(*user)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "[ERROR] saving user to database; %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s FuseServer) DownloadFile(req *proto.DownloadRequest, stream grpc.ServerStreamingServer[proto.FileChunk]) error {
	log.Printf("[GRPC] DownloadFile \"%v\"\n", req.Path)

	baseDir, err := s.getUsersBaseDir(stream.Context())
	if err != nil {
		return err
	}

	path := filepath.Join(baseDir, req.Path)
	stat := syscall.Stat_t{}
	err = syscall.Stat(path, &stat)
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

	file, err := os.Open(path)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	defer file.Close()

	// Hash local file and compare with received hash
	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	digest := hash.Sum(nil)
	fileHash := hex.EncodeToString(digest)
	if fileHash == req.ExpectedHash {
		// File hashes match; no need to send the file over network
		return nil
	}

	// Reset file's read pointer to start of file to prepare for second
	// read
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

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

	log.Printf("[DEBUG] Sent %v bytes over network\n", sentBytes)
	return nil
}

// FUSE functions

func (s FuseServer) Attr(ctx context.Context, req *proto.Node) (*proto.FileAttr, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(baseDir, req.Path)
	// log.Printf("[GRPC] Attr \"%v\"\n", s.relativePath(path))

	stat := syscall.Stat_t{}
	err = syscall.Lstat(path, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return lib.StatToFileAttr(&stat), nil
}

func (s FuseServer) Lookup(ctx context.Context, req *proto.LookupRequest) (*proto.Node, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] Lookup \"%v\"\n", s.relativePath(path))

	info, err := os.Stat(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	attr := lib.FileInfoToFileAttr(info)
	return &proto.Node{
		Path: req.Path,
		Attr: attr,
	}, nil
}

func (s FuseServer) ReadDirAll(ctx context.Context, req *proto.Node) (*proto.ReadDirAllResponse, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] ReadDirAll \"%v\"\n", s.relativePath(path))

	files, err := os.ReadDir(path)
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

		attr := lib.FileInfoToFileAttr(info)
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

func (s FuseServer) Mkdir(ctx context.Context, req *proto.MkdirRequest) (*proto.Node, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] Mkdir \"%v\"\n", s.relativePath(path))

	err = os.Mkdir(path, os.FileMode(req.Mode))
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
		Attr: lib.StatToFileAttr(&stat),
	}, nil
}

func (s FuseServer) Rmdir(ctx context.Context, req *proto.Node) (*emptypb.Empty, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] Rmdir \"%v\"\n", s.relativePath(path))

	err = os.Remove(path)
	if err != nil {
		code := codes.Internal
		if os.IsNotExist(err) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code, err.Error())
	}
	return &emptypb.Empty{}, nil
}

func (s FuseServer) Getattr(ctx context.Context, req *proto.Node) (*proto.FileAttr, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] Getattr \"%v\"\n", s.relativePath(path))

	stat := syscall.Stat_t{}
	err = syscall.Lstat(path, &stat)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return lib.StatToFileAttr(&stat), nil
}

func (s FuseServer) Create(ctx context.Context, req *proto.CreateRequest) (*proto.CreateResponse, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] Create \"%v\"\n", s.relativePath(path))

	file, err := os.OpenFile(path, int(req.Flags), os.FileMode(req.Mode))
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	attr := lib.FileInfoToFileAttr(info)
	return &proto.CreateResponse{
		NodeId: attr.Inode,
		Attr:   attr,
	}, nil
}

func (s FuseServer) Symlink(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}

	oldpath := filepath.Join(baseDir, req.OldPath)
	newpath := filepath.Join(baseDir, req.NewPath)
	log.Printf("[GRPC] Symlink %v -> %v\n", s.relativePath(oldpath), s.relativePath(newpath))

	err = syscall.Symlink(oldpath, newpath)
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
			Attr: lib.StatToFileAttr(&stat),
		},
	}, nil
}

func (s FuseServer) Link(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}

	oldpath := filepath.Join(baseDir, req.OldPath)
	newpath := filepath.Join(baseDir, req.NewPath)
	log.Printf("[GRPC] Link %v -> %v\n", s.relativePath(oldpath), s.relativePath(newpath))

	err = syscall.Link(oldpath, newpath)
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
			Attr: lib.StatToFileAttr(&stat),
		},
	}, nil
}

func (s FuseServer) ReadAll(ctx context.Context, req *proto.Node) (*proto.ReadAllResponse, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(baseDir, req.Path)
	log.Printf("[GRPC] ReadAll %v\n", s.relativePath(path))

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &proto.ReadAllResponse{Data: data}, nil
}

func (FuseServer) Write(context.Context, *proto.WriteRequest) (*proto.WriteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Write not implemented")
}

func (s FuseServer) Rename(ctx context.Context, req *proto.RenameRequest) (*emptypb.Empty, error) {
	baseDir, err := s.getUsersBaseDir(ctx)
	if err != nil {
		return nil, err
	}

	oldpath := filepath.Join(baseDir, req.OldPath)
	newpath := filepath.Join(baseDir, req.NewPath)
	log.Printf("[GRPC] Rename %v -> %v\n", s.relativePath(oldpath), s.relativePath(newpath))

	err = syscall.Rename(oldpath, newpath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &emptypb.Empty{}, nil
}
