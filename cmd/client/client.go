package main

import (
	"context"
	"flag"
	"io"
	"strings"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"go.uber.org/zap"
)

var (
	socket = flag.String("socket", "checker.sock", "The unix socket for health checker")
)

func main() {
	flag.Parse()

	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck

	var sock string
	if strings.Contains(*socket, "/") {
		sock = "unix://" + *socket
	} else {
		sock = "unix:" + *socket
	}

	logger.Infof("using socket %s", *socket)

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(sock, opts...)
	if err != nil {
		logger.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	client := pb.NewSpyreHealthServiceClient(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})

	if err != nil {
		cancel()
		logger.Fatalf("client.client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
	}

	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			cancel()
			logger.Fatalf("client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
		}

		if len(deviceList.Devices) == 0 {
			logger.Infof("Query did not identify any supported devices.")
		}

		for _, d := range deviceList.Devices {
			logger.Infof("  PCIAddress=%s  Type=%s  State=%s",
				d.GetDeviceID().GetPCIAddress(),
				d.GetDeviceType().String(),
				d.GetDeviceState().String(),
			)
		}
	}
}
