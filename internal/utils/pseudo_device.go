package utils

import (
	"os"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

var (
	GoodCards = []string{
		"0000:1a:00.0",
		"0000:1c:00.0",
		"0000:1d:00.0",
		"0000:1e:00.0",
		"0000:3d:00.0",
		"0000:3f:00.0",
		"0000:40:00.0",
	}
	BadCards = []string{
		"0000:41:00.0",
	}
)

const (
	PseudoDeviceModeKey = "PSEUDO_DEVICE_MODE"
	ModeEnabledValue    = "1"
)

func IsPseudoDeviceMode() bool {
	return os.Getenv(PseudoDeviceModeKey) == ModeEnabledValue
}

// GetPseudoDeviceHealths returns static list of card healths,
// according to predefined GoodCards and BadCards in this module.
//
// Pseudo devices are listed from PseudoTopology in pkg/utils/spyre.go.
func GetPseudoDeviceHealths() (healths []types.DeviceState) {
	for _, card := range GoodCards {
		healths = append(healths, types.DeviceState{PciAddress: card, State: pb.DEVICE_STATE_ONLINE})
	}
	for _, card := range BadCards {
		healths = append(healths, types.DeviceState{PciAddress: card, State: pb.DEVICE_STATE_IN_ERROR})
	}
	return healths
}
