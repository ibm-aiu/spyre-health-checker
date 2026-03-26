package healthcheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
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

// fileIsAccessible returns true if os.Stat(path) succeeds within 5 seconds.
// It returns false if Stat() errors for any reason or if it takes longer than 5 seconds.
func fileIsAccessible(path string) bool {
	type result struct {
		ok bool
	}
	ch := make(chan result, 1)

	go func() {
		_, err := os.Stat(path)
		ch <- result{ok: err == nil}
	}()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case r := <-ch:
		return r.ok
	case <-timer.C:
		return false
	}
}

// updateDriverStatus() will set the state of a specific card to DEVICE_STATE_IN_ERROR
// if it is not accessible for any reason, or if there is a timeout trying verify access.
func updateDriverStatus(states []types.DeviceState) {
	for i := range states {
		driverPath := filepath.Join("/sys/bus/pci/devices", states[i].PciAddress, "driver")
		if !fileIsAccessible(driverPath) {
			states[i].State = pb.DEVICE_STATE_IN_ERROR
		}
	}
}

// UpdateStates updates device states.
func (v *Vitals) UpdateStates() error {
	var states []types.DeviceState
	if utils.IsPseudoDeviceMode() {
		// do not check for drivers in pseudo case
		states = utils.GetPseudoDeviceHealths()
	} else {
		var err error
		states, err = v.runLSPCI()
		if err != nil {
			return err
		}
		updateDriverStatus(states)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.States = states
	return nil
}
