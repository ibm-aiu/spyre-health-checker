package utils

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

const (
	sriovVFArch = "s390x"
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
	GoodIsolatedVFCards = []string{
		"0001:00:00.0",
		"0002:00:00.0",
		"0003:00:00.0",
		"0004:00:00.0",
		"0005:00:00.0",
		"0006:00:00.0",
		"0007:00:00.0",
	}
	BadIsolatedVFCards = []string{
		"0008:00:00.0",
	}
)

const (
	PseudoDeviceModeKey = "PSEUDO_DEVICE_MODE"
	ModeEnabledValue    = "1"
)

var (
	PseudoRuntimeArch = runtime.GOARCH
)

func IsPseudoDeviceMode() bool {
	return os.Getenv(PseudoDeviceModeKey) == ModeEnabledValue
}

// GetPseudoDeviceHealths returns static list of card healths,
// according to predefined GoodCards and BadCards in this module.
//
// Pseudo devices are align with pseudo device in device plugin.
func GetPseudoDeviceHealths() (healths []types.DeviceState) {
	for _, card := range GoodCards {
		healths = append(healths, types.DeviceState{PciAddress: card, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE})
		if PseudoRuntimeArch != sriovVFArch {
			vf1 := getPseudoVfAddress(card, 1)
			vf2 := getPseudoVfAddress(card, 2)
			healths = append(healths, types.DeviceState{PciAddress: vf1, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE})
			healths = append(healths, types.DeviceState{PciAddress: vf2, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE})
		}
	}
	for _, card := range BadCards {
		healths = append(healths, types.DeviceState{PciAddress: card, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR})
		if PseudoRuntimeArch != sriovVFArch {
			vf1 := getPseudoVfAddress(card, 1)
			vf2 := getPseudoVfAddress(card, 2)
			healths = append(healths, types.DeviceState{PciAddress: vf1, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_IN_ERROR})
			healths = append(healths, types.DeviceState{PciAddress: vf2, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_IN_ERROR})
		}
	}
	if PseudoRuntimeArch == sriovVFArch {
		for _, card := range GoodIsolatedVFCards {
			healths = append(healths, types.DeviceState{PciAddress: card, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE})
		}
		for _, card := range BadIsolatedVFCards {
			healths = append(healths, types.DeviceState{PciAddress: card, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_IN_ERROR})
		}
	}

	return healths
}

func getPseudoVfAddress(pfAddress string, vfIndex int) string {
	if vfIndex < 1 {
		return ""
	}
	splits := strings.Split(pfAddress, ".")
	return fmt.Sprintf("%s.%d", splits[0], vfIndex)
}
