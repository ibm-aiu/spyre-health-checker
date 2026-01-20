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
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

var (
	socket = flag.String("socket", "/usr/local/etc/device-plugins/health/checker.sock", "The server unix socket")
	timer  = flag.String("timer", "1h", "Run all tests periodically on each node. Time set in interval format. Defaults to 1h")
	logDir = flag.String("logdir", "", "Directory to save log events")
	v      = flag.String("loglevel", "1", "Log level")
)

func main() {
	flag.Parse()

	if err := flag.Set("alsologtostderr", "true"); err != nil {
		glog.Errorf("Error setting alsologtostderr: ", err)
		os.Exit(1)
	}
	if *logDir != "" {
		if err := flag.Set("log_dir", *logDir); err != nil {
			glog.Errorf("Error setting log_dir: %v", err)
			os.Exit(1)
		}
	}
	if err := flag.Set("v", *v); err != nil {
		glog.Errorf("Error setting v: ", err)
		os.Exit(1)
	}
	if err := flag.Set("logtostderr", "false"); err != nil {
		glog.Errorf("Error setting logtostderr: ", err)
		os.Exit(1)
	}

	vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}

	glog.V(1).Infof("loglevel: debug")
	s := server.NewServer(&vitals)

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

	periodicChecksTicker := time.NewTicker(timer)
	defer periodicChecksTicker.Stop()
	for range periodicChecksTicker.C {
		vitals.UpdateStates()
		s.UpdateHealths(vitals.GetVitalStates())
		// todo: update prometheus registry data here with status information from vitals structure
	}
}
