package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	healthchecks "ibm.com/vitals/pkg/healthChecks"
	pb "ibm.com/vitals/pkg/proto/spyre_health"
	spyrehealthserver "ibm.com/vitals/pkg/spyreHealthServer"
	utils "ibm.com/vitals/pkg/utils"
)

var (
	port    = flag.Int("port", 50051, "The server port")
	timer   = flag.String("timer", "1h", "Run all tests periodically on each node. Time set in interval format. Defaults to 1h")
	logFile = flag.String("logfile", "", "File where to save all the events")
	v       = flag.String("loglevel", "1", "Log level")
)

func main() {
	flag.Parse()

	flag.Set("alsologtostderr", "true")
	if *logFile != "" {
		flag.Set("log_file", *logFile)
	}
	flag.Set("v", *v)
	flag.Set("logtostderr", "false")

	glog.V(1).Infof("loglevel: debug")
	glog.V(1).Infof("Starting gRPC server")
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", *port))
	if err != nil {
		glog.Errorf("failed to listen: %v", err)

	}
	grpcServer := grpc.NewServer()

	s := spyrehealthserver.NewServer()
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go grpcServer.Serve(lis)

	glog.V(1).Infof("Starting timer for periodic checks")
	// Parse the repeat and invasive intervals to durations
	timer, err := utils.ParseInterval(*timer)
	if err != nil {
		glog.Errorf("Error parsing repeat interval: ", err)
		os.Exit(1)
	}

	reg := prometheus.NewRegistry()
	utils.InitMetrics(reg)

	vitals := healthchecks.Vitals{}

	periodicChecksTicker := time.NewTicker(timer)
	defer periodicChecksTicker.Stop()
	for {
		select {
		case <-periodicChecksTicker.C:
			vitals.RunLSPCI()
		}
	}

}
