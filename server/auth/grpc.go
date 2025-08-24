package auth

import (
	"context"
	"slices"
	"strings"

	"github.com/caleb-mwasikira/fusion/server/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type key string

const (
	USER_CTX_KEY key = "USER"
)

var (
	nonProtectedMethods []string = []string{"Auth", "CreateOrg", "CreateUser"}
)

// Each gRPC request (except some non-protected methods) is going to embed a
// json web token in the request metadata for authentication.
// It is the work of this interceptor to check if the embedded
// json web token is valid
func AuthInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp any, err error) {
	skipAuth := slices.ContainsFunc(
		nonProtectedMethods,
		func(method string) bool {
			return strings.Contains(info.FullMethod, method)
		},
	)
	if skipAuth {
		return handler(ctx, req)
	}

	// log.Printf("[DEBUG] Authenticating method %v\n", info.FullMethod)

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "Missing metadata")
	}

	tokens, ok := md["authorization"]
	if !ok || len(tokens) == 0 {
		return nil, status.Error(codes.Unauthenticated, "Missing authorization key in metadata")
	}
	token := tokens[0]

	var user db.User
	if !ValidToken(token, &user) {
		return nil, status.Error(codes.Unauthenticated, "Invalid authorization token")
	}

	// Save user object into context
	newCtx := context.WithValue(ctx, USER_CTX_KEY, &user)
	return handler(newCtx, req)
}

type myServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Get our own context instead of the ServerStream context
func (ss myServerStream) Context() context.Context {
	return ss.ctx
}

func AuthStreamInterceptor(
	srv any,
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	// log.Printf("[DEBUG] Authenticating stream method %v\n", info.FullMethod)

	md, ok := metadata.FromIncomingContext(ss.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "Missing metadata")
	}

	tokens, ok := md["authorization"]
	if !ok || len(tokens) == 0 {
		return status.Error(codes.Unauthenticated, "Missing authorization key in metadata")
	}
	token := tokens[0]

	var user db.User
	if !ValidToken(token, &user) {
		return status.Error(codes.Unauthenticated, "Invalid authorization token")
	}

	// Save user object into context
	newCtx := context.WithValue(ss.Context(), USER_CTX_KEY, &user)
	newServerStream := myServerStream{
		ServerStream: ss,
		ctx:          newCtx,
	}
	return handler(srv, newServerStream)
}
