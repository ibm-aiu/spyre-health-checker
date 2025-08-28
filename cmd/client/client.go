package main

import (
	"context"
	"flag"
	"io"
	"log"
	"time"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	serverAddr = flag.String("addr", "localhost:50051", "The server address in the format of host:port")
)

func main() {
	flag.Parse()
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	client := pb.NewSpyreHealthServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})

	if err != nil {
		cancel()
		log.Fatalf("client.client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
	}

	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			cancel()
			log.Fatalf("client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
		}
		log.Println("Devices:\n", deviceList.Devices)
	}
}
