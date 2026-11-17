/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package utils

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// One series per device-state tuple that exists in vitals.States.
var SpyreDeviceState *prometheus.GaugeVec

// SpyreHealthOverride has exactly one series, set to 1, naming the node health override in effect.
var SpyreHealthOverride *prometheus.GaugeVec

func newHealthOverrideGauge() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "spyre",
			Name:      "health_override",
			Help: "Node health override in effect (ConfigMap spyre-health-override): " +
				"mode is none, healthy, unhealthy or invalid.",
		},
		[]string{"node", "mode"},
	)
}

func newDeviceStateGauge() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "spyre",
			Name:      "device_state",
			Help:      "Current state for each Spyre device.",
		},
		[]string{"node", "pci_address", "devicetype", "state", "source"},
	)
}

// registerGauge registers g on reg, or returns the gauge that is already registered.
func registerGauge(reg prometheus.Registerer, g *prometheus.GaugeVec) *prometheus.GaugeVec {
	err := reg.Register(g)
	if err == nil {
		return g
	}
	if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
		return are.ExistingCollector.(*prometheus.GaugeVec)
	}
	panic(err)
}

// InitMetrics registers metrics and built-in collectors on the provided Registerer.
func InitMetrics(reg prometheus.Registerer) {
	SpyreDeviceState = registerGauge(reg, newDeviceStateGauge())
	SpyreHealthOverride = registerGauge(reg, newHealthOverrideGauge())

	_ = reg.Register(collectors.NewGoCollector())
	_ = reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

}

// UpdateDeviceMetrics clears previous series and sets one series per state entry.
func UpdateDeviceMetrics(states []types.DeviceState) {
	SpyreDeviceState.Reset()
	node := NodeName

	for _, s := range states {
		SpyreDeviceState.With(prometheus.Labels{
			"node":        node,
			"pci_address": s.PciAddress,
			"devicetype":  enumDeviceType(s.Type),
			"state":       enumDeviceState(s.State),
			"source":      s.Source,
		}).Set(1)
	}
}

// UpdateOverrideMetric replaces the series of spyre_health_override with one series for mode.
func UpdateOverrideMetric(mode string) {
	if SpyreHealthOverride == nil {
		return
	}
	SpyreHealthOverride.Reset()
	SpyreHealthOverride.With(prometheus.Labels{"node": NodeName, "mode": mode}).Set(1)
}

func enumDeviceType(t pb.DEVICE_TYPE) string {
	switch t {
	case pb.DEVICE_TYPE_PF:
		return "PF"
	case pb.DEVICE_TYPE_VF:
		return "VF"
	default:
		return "UNSPECIFIED"
	}
}

func enumDeviceState(st pb.DEVICE_STATE) string {
	switch st {
	case pb.DEVICE_STATE_OFFLINE:
		return "OFFLINE"
	case pb.DEVICE_STATE_BOOTING:
		return "BOOTING"
	case pb.DEVICE_STATE_SHUTTING_DOWN:
		return "SHUTTING_DOWN"
	case pb.DEVICE_STATE_ONLINE:
		return "ONLINE"
	case pb.DEVICE_STATE_RUNNING_DIAGNOSTICS:
		return "RUNNING_DIAGNOSTICS"
	case pb.DEVICE_STATE_IN_ERROR:
		return "IN_ERROR"
	case pb.DEVICE_STATE_REMOVED:
		return "REMOVED"
	default:
		return "UNSPECIFIED"
	}
}
