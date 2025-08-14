package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/lib/proto"
	"github.com/caleb-mwasikira/fusion/server/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type FuseServer struct {
	proto.UnimplementedFuseServer

	// Real path to where files are stored
	path string
}

func NewFuseServer(ctx context.Context, path string) FuseServer {
	go startMainObserver(ctx)

	return FuseServer{
		path: mountpoint,
	}
}

var _ = (proto.FuseServer)((*FuseServer)(nil))

// Gets the logged in user's root directory
//
//	returns:
//		string: path they are allowed access to
//		error: if access is denied
func getUsersDir(ctx context.Context) (string, error) {
	user, ok := ctx.Value(userCtxKey).(*db.User)
	if !ok {
		// Usr is NOT logged in
		// The system should never reach this state as we are relying on the
		// auth interceptor to filter unauthenticated gRPC requests
		return "", errors.New("user not logged in")
	}

	fullpath := filepath.Join(mountpoint, user.OrgName, user.DeptName)

	// Check if directory exists
	stat := syscall.Stat_t{}
	err := syscall.Stat(fullpath, &stat)
	if err != nil {
		return "", err
	}

	return relativePath(fullpath), nil
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
		return nil, grpcError(err)
	}

	if !isEmpty(req.DeptName) {
		// Create department directory
		deptDir := filepath.Join(baseDir, req.DeptName)
		err := os.MkdirAll(deptDir, 0771)
		if err != nil {
			return nil, grpcError(err)
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
	// log.Printf("[GRPC] DownloadFile \"%v\"\n", req.Path)

	ctx := stream.Context()
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return grpcError(err)
	}

	fullpath := filepath.Join(s.path, usersDir, req.Path)
	file, err := os.Open(fullpath)
	if err != nil {
		return grpcError(err)
	}
	defer file.Close()

	// Hash local file and compare with received hash
	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return grpcError(err)
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
		return grpcError(err)
	}

	info, err := file.Stat()
	if err != nil {
		return grpcError(err)
	}

	buff := make([]byte, 64*1024) // 64Kb
	sentBytes := 0

outer:
	for {
		select {
		case <-ctx.Done():
			// Client closed connection
			break outer

		default:
			n, err := file.Read(buff)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return grpcError(err)
			}

			chunk := proto.FileChunk{
				Data:      buff[:n],
				Offset:    int64(sentBytes),
				TotalSize: info.Size(),
			}
			err = stream.Send(&chunk)
			if err != nil {
				return grpcError(err)
			}

			sentBytes += n
		}
	}

	return nil
}

func (s FuseServer) ObserveFileChanges(_ *emptypb.Empty, stream grpc.ServerStreamingServer[proto.FileEvent]) error {
	ctx := stream.Context()
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return grpcError(err)
	}

	log.Printf("[GRPC] Client observing MAIN_OBSERVER@%v\n", usersDir)
	clientChan := make(chan *proto.FileEvent, 10)

	// Add user as an observer
	mu.Lock()
	observers[usersDir] = append(observers[usersDir], clientChan)
	mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			// Client closed connection
			log.Printf("[GRPC] Client stopped observing MAIN_OBSERVER@%v; %v\n", usersDir, ctx.Err())
			return nil

		case fileEvent := <-clientChan:
			log.Printf("[GRPC] Sending file event %s to client\n", fileEvent)

			// Trim usersDir from response; our clients do NOT care
			// how the directories are structured on the backend
			trimmedPath := strings.TrimPrefix(fileEvent.Path, usersDir)
			fileEvent.Path = trimmedPath

			trimmedPath = strings.TrimPrefix(fileEvent.NewPath, usersDir)
			fileEvent.NewPath = trimmedPath

			err := stream.Send(fileEvent)
			if err != nil {
				return grpcError(err)
			}
		}
	}
}

// FUSE functions

func (s FuseServer) Attr(ctx context.Context, req *proto.DirEntry) (*proto.FileAttr, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	fullpath := filepath.Join(s.path, usersDir, req.Path)
	// log.Printf("[GRPC] Attr \"%v\"\n", relativePath(fullpath))

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		return nil, grpcError(err)
	}
	return lib.StatToFileAttr(&stat), nil
}

func (s FuseServer) Lookup(ctx context.Context, req *proto.LookupRequest) (*proto.DirEntry, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Lookup \"%v\"\n", relativePath(fullpath))

	stat := syscall.Stat_t{}
	err = syscall.Stat(fullpath, &stat)
	if err != nil {
		return nil, grpcError(err)
	}

	return &proto.DirEntry{
		Path: req.Path,
		Attr: lib.StatToFileAttr(&stat),
	}, nil
}

func (s FuseServer) ReadDirAll(ctx context.Context, req *proto.DirEntry) (*proto.ReadDirAllResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	// log.Printf("[GRPC] ReadDirAll \"%v\"\n", relativePath(fullpath))

	files, err := os.ReadDir(fullpath)
	if err != nil {
		return nil, grpcError(err)
	}

	entries := []*proto.DirEntry{}
	for _, file := range files {
		filePath := filepath.Join(req.Path, file.Name())

		info, err := file.Info()
		if err != nil {
			continue
		}

		attr := lib.FileInfoToFileAttr(info)
		entries = append(entries, &proto.DirEntry{
			Ino:  attr.Ino,
			Path: filePath,
			Mode: uint32(info.Mode()),
		})
	}
	return &proto.ReadDirAllResponse{
		Entries: entries,
	}, nil
}

func (s FuseServer) Mkdir(ctx context.Context, req *proto.MkdirRequest) (*proto.DirEntry, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Mkdir \"%v\"\n", relativePath(fullpath))

	err = os.Mkdir(fullpath, os.FileMode(req.Mode))
	if err != nil {
		return nil, grpcError(err)
	}

	// Confirm directory was created
	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		os.Remove(fullpath)
		return nil, grpcError(err)
	}

	return &proto.DirEntry{
		Path: req.Path,
		Attr: lib.StatToFileAttr(&stat),
	}, nil
}

func (s FuseServer) Rmdir(ctx context.Context, req *proto.DirEntry) (*emptypb.Empty, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Rmdir \"%v\"\n", relativePath(fullpath))

	err = os.Remove(fullpath)
	if err != nil {
		return nil, grpcError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s FuseServer) Getattr(ctx context.Context, req *proto.DirEntry) (*proto.FileAttr, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Getattr \"%v\"\n", relativePath(fullpath))

	stat := syscall.Stat_t{}
	err = syscall.Lstat(fullpath, &stat)
	if err != nil {
		return nil, grpcError(err)
	}
	return lib.StatToFileAttr(&stat), nil
}

func (s FuseServer) Create(ctx context.Context, req *proto.CreateRequest) (*proto.CreateResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}
	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Create \"%v\"\n", relativePath(fullpath))

	file, err := os.OpenFile(fullpath, int(req.Flags), os.FileMode(req.Mode))
	if err != nil {
		return nil, grpcError(err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, grpcError(err)
	}
	attr := lib.FileInfoToFileAttr(info)
	return &proto.CreateResponse{
		NodeId: attr.Ino,
		Attr:   attr,
	}, nil
}

func (s FuseServer) Symlink(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	oldpath := filepath.Join(s.path, usersDir, req.OldPath)
	newpath := filepath.Join(s.path, usersDir, req.NewPath)
	log.Printf("[GRPC] Symlink %v -> %v\n", relativePath(oldpath), relativePath(newpath))

	err = syscall.Symlink(oldpath, newpath)
	if err != nil {
		return nil, grpcError(err)
	}

	// Stat new path
	stat := syscall.Stat_t{}
	err = syscall.Lstat(newpath, &stat)
	if err != nil {
		return nil, grpcError(err)
	}

	return &proto.LinkResponse{
		Node: &proto.DirEntry{
			Path: req.NewPath,
			Attr: lib.StatToFileAttr(&stat),
		},
	}, nil
}

func (s FuseServer) Link(ctx context.Context, req *proto.LinkRequest) (*proto.LinkResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	oldpath := filepath.Join(s.path, usersDir, req.OldPath)
	newpath := filepath.Join(s.path, usersDir, req.NewPath)
	log.Printf("[GRPC] Link %v -> %v\n", relativePath(oldpath), relativePath(newpath))

	err = syscall.Link(oldpath, newpath)
	if err != nil {
		return nil, grpcError(err)
	}

	// Stat new path
	stat := syscall.Stat_t{}
	err = syscall.Stat(newpath, &stat)
	if err != nil {
		return nil, grpcError(err)
	}

	return &proto.LinkResponse{
		Node: &proto.DirEntry{
			Path: req.NewPath,
			Attr: lib.StatToFileAttr(&stat),
		},
	}, nil
}

func (s FuseServer) ReadAll(ctx context.Context, req *proto.DirEntry) (*proto.ReadAllResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] ReadAll %v\n", relativePath(fullpath))

	data, err := os.ReadFile(fullpath)
	if err != nil {
		return nil, grpcError(err)
	}
	return &proto.ReadAllResponse{Data: data}, nil
}

func (s FuseServer) Write(ctx context.Context, req *proto.WriteRequest) (*proto.WriteResponse, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	fullpath := filepath.Join(s.path, usersDir, req.Path)
	log.Printf("[GRPC] Write %v bytes of data to file %v\n", len(req.Data), req.Path)

	file, err := os.OpenFile(fullpath, os.O_WRONLY, 0755)
	if err != nil {
		return nil, grpcError(err)
	}
	defer file.Close()

	n, err := file.WriteAt(req.Data, req.Offset)
	if err != nil {
		return nil, grpcError(err)
	}

	return &proto.WriteResponse{
		BytesWritten: uint64(n),
	}, nil
}

func (s FuseServer) Rename(ctx context.Context, req *proto.RenameRequest) (*emptypb.Empty, error) {
	usersDir, err := getUsersDir(ctx)
	if err != nil {
		return nil, grpcError(err)
	}

	oldpath := filepath.Join(s.path, usersDir, req.OldPath)
	newpath := filepath.Join(s.path, usersDir, req.NewPath)
	log.Printf("[GRPC] Rename %v -> %v\n", relativePath(oldpath), relativePath(newpath))

	newParentDir := filepath.Dir(newpath)
	if _, err := os.Stat(newParentDir); os.IsNotExist(err) {
		log.Printf("[GRPC] Target directory '%s' does not exist. Creating it.\n", newParentDir)
		err := os.MkdirAll(newParentDir, 0755)
		if err != nil {
			log.Printf("[GRPC] Failed to create target directory: %v\n", err)
			return nil, grpcError(err)
		}
	}

	err = syscall.Rename(oldpath, newpath)
	if err != nil {
		return nil, grpcError(err)
	}
	return &emptypb.Empty{}, nil
}

// Parse normal error into GRPC error code
func grpcError(err error) error {
	switch {
	case os.IsNotExist(err):
		return status.Error(codes.NotFound, err.Error())

	case os.IsPermission(err):
		return status.Error(codes.PermissionDenied, err.Error())

	case os.IsTimeout(err):
		return status.Error(codes.DeadlineExceeded, err.Error())

	case os.IsExist(err):
		return status.Error(codes.AlreadyExists, err.Error())

	case err == syscall.EIO:
		// I/O error, often indicates a physical disk failure
		return status.Errorf(codes.Internal, "I/O error: %v", err)

	case err == syscall.ENOSPC:
		// No space left on device
		return status.Errorf(codes.ResourceExhausted, "no space left on device: %v", err)

	case err == syscall.EINVAL:
		// Invalid argument to a syscall
		return status.Errorf(codes.InvalidArgument, "invalid system call argument: %v", err)

	default:
		return status.Error(codes.Internal, err.Error())
	}
}
