package main

import (
	"flag"
	"os"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	healthcheck "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/healthcheck"
	utils "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	server "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/server"
)

var (
	socket  = flag.String("socket", "/usr/local/etc/device-plugins/health/checker.sock", "The server unix socket")
	timer   = flag.String("timer", "1h", "Run all tests periodically on each node. Time set in interval format. Defaults to 1h")
	logFile = flag.String("logfile", "", "File where to save all the events")
	v       = flag.String("loglevel", "1", "Log level")
)

func main() {
	flag.Parse()

	if err := flag.Set("alsologtostderr", "true"); err != nil {
		glog.Errorf("Error set alsologtostderr: ", err)
		os.Exit(1)
	}
	if *logFile != "" {
		if err := flag.Set("log_file", *logFile); err != nil {
			glog.Errorf("Error set log_file: ", err)
			os.Exit(1)
		}
	}
	if err := flag.Set("v", *v); err != nil {
		glog.Errorf("Error set v: ", err)
		os.Exit(1)
	}
	if err := flag.Set("logtostderr", "false"); err != nil {
		glog.Errorf("Error set logtostderr: ", err)
		os.Exit(1)
	}

	glog.V(1).Infof("loglevel: debug")
	s := server.NewServer()

	glog.V(1).Infof("Starting gRPC server")
	if err := s.StartGRPCServer(*socket); err != nil {
		glog.Fatal(err)
	}

	glog.V(1).Infof("Starting timer for periodic checks")
	// Parse the repeat and invasive intervals to durations
	timer, err := utils.ParseInterval(*timer)
	if err != nil {
		glog.Errorf("Error parsing repeat interval: ", err)
		s.Stop()
		os.Exit(1)
	}
	defer s.Stop()

	reg := prometheus.NewRegistry()
	utils.InitMetrics(reg)

	vitals := healthcheck.Vitals{}

	periodicChecksTicker := time.NewTicker(timer)
	defer periodicChecksTicker.Stop()
	for range periodicChecksTicker.C {
		vitals.RunLSPCI()
	}
}
