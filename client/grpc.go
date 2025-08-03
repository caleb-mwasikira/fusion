package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/caleb-mwasikira/fusion/proto"
	"github.com/caleb-mwasikira/fusion/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// Returns an authenticated gRPC client
func New_gRPC_Client() proto.FuseClient {
	certFile := filepath.Join(utils.CertDir, "ca.crt")
	transportCreds, err := credentials.NewClientTLSFromFile(certFile, "")
	if err != nil {
		log.Fatalf("[ERROR] generating gRPC client credentials; %v\n", err)
	}

	// Setup an unauthenticated connection
	conn, err := grpc.NewClient(
		remote,
		grpc.WithTransportCredentials(transportCreds),
	)
	if err != nil {
		log.Fatalf("[ERROR] creating GRPC channel; %v\n", err)
	}

	return proto.NewFuseClient(conn)
}

// Embeds authorization key in gRPC request metadata
func NewAuthenticatedCtx(ctx context.Context) context.Context {
	md := metadata.New(map[string]string{
		"authorization": authToken,
	})
	return metadata.NewOutgoingContext(ctx, md)
}
