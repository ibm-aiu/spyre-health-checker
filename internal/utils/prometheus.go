package utils

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	HchecksGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "health_checks",
			Help:      "Summary of the health checks measurements on compute nodes. Gauge Vector version",
		},
		[]string{"health", "node", "deviceid", "devicetype", "devicestate"},
	)
)

func InitMetrics(reg prometheus.Registerer) {
	// Register custom metrics with the global prometheus registry
	reg.MustRegister(HchecksGauge)
}
