package utils

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

// One series per device-state tuple that exists in vitals.States.
var (
	SpyreDeviceState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: "spyre",
			Name:      "device_state",
			Help:      "Current state for each Spyre device.",
		},
		[]string{"node", "deviceid", "devicetype", "state"},
	)
)

// InitMetrics registers metrics and built-in collectors on the provided Registerer.
func InitMetrics(reg prometheus.Registerer) {

	if err := reg.Register(SpyreDeviceState); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			// Reuse the one that’s already registered.
			SpyreDeviceState = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			panic(err)
		}
	}

	_ = reg.Register(collectors.NewGoCollector())
	_ = reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

}

// UpdateDeviceMetrics clears previous series and sets one series per state entry.
func UpdateDeviceMetrics(states []types.DeviceState) {
	SpyreDeviceState.Reset()
	node := NodeName

	for _, s := range states {
		SpyreDeviceState.With(prometheus.Labels{
			"node":       node,
			"deviceid":   s.PciAddress,
			"devicetype": enumDeviceType(s.Type),
			"state":      enumDeviceState(s.State),
		}).Set(1)
	}
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
