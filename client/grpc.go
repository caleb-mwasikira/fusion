package main

import (
	"context"
	"crypto/x509"
	_ "embed"
	"log"

	"github.com/caleb-mwasikira/fusion/lib/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

//go:embed certs/ca.crt
var CA_CERT_DATA []byte

// Returns an authenticated gRPC client
func new_gRPC_client() proto.FuseClient {
	certPool := x509.NewCertPool()
	ok := certPool.AppendCertsFromPEM(CA_CERT_DATA)
	if !ok {
		log.Fatalln("Error loading custom CA cert file")
	}

	// Setup TLS connection
	transportCreds := credentials.NewClientTLSFromCert(certPool, "")
	conn, err := grpc.NewClient(
		remote,
		grpc.WithTransportCredentials(transportCreds),
	)
	if err != nil {
		log.Fatalf("Error creating GRPC channel; %v\n", err)
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
