package healthcheck

import (
	"fmt"
	"os/exec"
	"sync"

	"github.com/golang/glog"

	utils "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

type Vitals struct {
	States []types.DeviceState
	mu     sync.RWMutex
}

func (v *Vitals) GetVitalStates() []types.DeviceState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.States
}

// runLSPCI executes the `lspci` command and stores the output.
func (v *Vitals) runLSPCI() ([]types.DeviceState, error) {
	out, err := exec.Command("sh", "-c", "lspci -vvvnn").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Failed to run lspci: %v", err)
	}
	return parseLSPCI(string(out)), nil
}

// UpdateStates updates device states.
func (v *Vitals) UpdateStates() {
	var states []types.DeviceState
	if utils.IsPseudoDeviceMode() {
		states = utils.GetPseudoDeviceHealths()
		glog.Info(states)
	} else {
		var err error
		states, err = v.runLSPCI()
		if err != nil {
			glog.Errorf("Failed to UpdateStates: %v", err)
			return
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.States = states
}
