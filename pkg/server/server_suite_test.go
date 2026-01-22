package server

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	healthcheck "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/healthcheck"
	utils "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
)

var (
	TestSocket = "checker.sock"

	TestHealthServer *healthServer
)

type Client struct {
	client  pb.SpyreHealthServiceClient
	mu      sync.RWMutex
	devices []*pb.Device
	conn    *grpc.ClientConn
	cancel  context.CancelFunc
}

func NewClient() *Client {
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
	Expect(err).To(BeNil())
	client := pb.NewSpyreHealthServiceClient(conn)
	return &Client{
		client: client,
		conn:   conn,
	}
}

func (c *Client) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	stream, err := c.client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})
	Expect(err).To(BeNil())
	go c.receive(stream)
}

func (c *Client) Stop() {
	c.cancel()
	c.conn.Close()
}

func (c *Client) receive(stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsClient) {
	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			return
		}

		select {
		case <-stream.Context().Done():
			return
		default:
			Expect(err).To(BeNil())
			c.mu.Lock()
			c.devices = deviceList.Devices
			c.mu.Unlock()
		}

	}
}

func (c *Client) GetHealths() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	healths := make(map[string]bool, len(c.devices))
	for _, device := range c.devices {
		healths[device.DeviceID.PCIAddress] = device.DeviceState == pb.DEVICE_STATE_ONLINE
	}
	return healths
}

func TestServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Spyre Health Checker Test Server Suite")
}

var _ = BeforeSuite(func() {
	os.Setenv(utils.PseudoDeviceModeKey, "1")

	ws := zapcore.AddSync(GinkgoWriter)

	encCfg := zap.NewDevelopmentEncoderConfig()
	enc := zapcore.NewConsoleEncoder(encCfg)

	core := zapcore.NewCore(enc, ws, zap.DebugLevel)
	uber := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer uber.Sync()

	crlog.SetLogger(zapr.NewLogger(uber))

	TestHealthServer = startServer()
})

var _ = AfterSuite(func() {
	err := os.RemoveAll(TestSocket)
	Expect(err).To(BeNil())
	err = os.Unsetenv(utils.PseudoDeviceModeKey)
	Expect(err).To(BeNil())
})

func startServer() *healthServer {
	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck
	SetLogger(logger)
	vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}
	s := NewServer(&vitals)
	err := s.StartGRPCServer(TestSocket)
	Expect(err).To(BeNil())
	return s
}
