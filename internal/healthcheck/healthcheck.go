package healthcheck

import (
	"fmt"
	"os/exec"
	"sync"

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
	out, err := exec.Command("sh", "-c", "lspci -vvvnn 2>/dev/null").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Failed to run lspci: %v", err)
	}
	return parseLSPCI(string(out)), nil
}

// UpdateStates updates device states.
func (v *Vitals) UpdateStates() error {
	var states []types.DeviceState
	if utils.IsPseudoDeviceMode() {
		states = utils.GetPseudoDeviceHealths()
	} else {
		var err error
		states, err = v.runLSPCI()
		if err != nil {
			return err
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.States = states
	return nil
}
