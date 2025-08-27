package main

import (
	"context"
	"flag"
	"io"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	pb "ibm.com/vitals/pkg/proto/spyre_health"
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
	defer conn.Close()

	client := pb.NewSpyreHealthServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})

	if err != nil {
		log.Fatalf("client.client.RegisterForSpyreDevicesEvents failed: %v", err)
	}

	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("client.RegisterForSpyreDevicesEvents failed: %v", err)
		}
		log.Println("Devices:\n", deviceList.Devices)
	}
}
