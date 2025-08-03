package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/caleb-mwasikira/fusion/server/db"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	userModel *db.UserModel = db.NewUserModel()

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
	isNonProtectedMethod := slices.ContainsFunc(
		nonProtectedMethods,
		func(method string) bool {
			return strings.Contains(info.FullMethod, method)
		},
	)
	if isNonProtectedMethod {
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
	if !validToken(token, &user) {
		return nil, status.Error(codes.Unauthenticated, "Invalid authorization token")
	}

	// Save user object into context
	newCtx := context.WithValue(ctx, userCtxKey, &user)
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
	if !validToken(token, &user) {
		return status.Error(codes.Unauthenticated, "Invalid authorization token")
	}

	// Save user object into context
	newCtx := context.WithValue(ss.Context(), userCtxKey, &user)
	newServerStream := myServerStream{
		ServerStream: ss,
		ctx:          newCtx,
	}
	return handler(srv, newServerStream)
}

func authUser(username, password string) (*db.User, bool) {
	user, err := userModel.Get(username)
	if err != nil {
		return nil, false
	}
	passwordMatch := verifyPassword(user.Password, password)
	return user, passwordMatch
}

func generateToken(user db.User) (string, error) {
	data, err := json.Marshal(user)
	if err != nil {
		return "", err
	}

	b64EncodedData := base64.StdEncoding.EncodeToString(data)
	now := time.Now()
	expiry := now.Add(72 * time.Hour)

	token := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		jwt.MapClaims{
			"iat": now.Unix(),
			"exp": expiry.Unix(),
			"iss": "fusion",
			"sub": b64EncodedData,
		},
	)
	tokenString, err := token.SignedString([]byte(SECRET_KEY))
	return tokenString, err
}

// Verifies a json web token and returns the object stored
// in "sub" subject field. expects obj parameter to be a pointer of type T
func validToken(tokenString string, obj any) bool {
	token, err := jwt.Parse(
		tokenString,
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(SECRET_KEY), nil
		},
	)
	if err != nil {
		log.Printf("Error parsing jwt; %v\n", err)
		return false
	}

	// read claims from payload and extract the b64 encoded data.
	claims, ok := token.Claims.(jwt.MapClaims)
	if ok && token.Valid {
		// get subject - stored as base64 data
		b64EncodedData, ok := claims["sub"].(string)
		if !ok {
			log.Println("Unexpected \"sub\" type in jwt")
			return false
		}

		// decode data
		data, err := base64.StdEncoding.DecodeString(b64EncodedData)
		if err != nil {
			log.Printf("Error decoding \"sub\" value of jwt; %v\n", err)
			return false
		}

		// unmarshal the data into the param object
		err = json.Unmarshal(data, obj)
		return err == nil
	}

	return false
}

// func hashUserPassword(password string) string {
// 	hash := hmac.New(sha256.New, []byte(SECRET_KEY))
// 	digest := hash.Sum([]byte(password))
// 	return fmt.Sprintf("%x", digest)
// }

func verifyPassword(dbPassword, password string) bool {
	hash := hmac.New(sha256.New, []byte(SECRET_KEY))
	mac2 := hash.Sum([]byte(password))
	hmacPassword, err := hex.DecodeString(dbPassword)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(hmacPassword), mac2)
}
