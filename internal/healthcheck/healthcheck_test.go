package healthcheck

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

var _ = Describe("HealthChecker functions", func() {
	It("GetVitalStates() returns v.States object", func() {
		vitals := Vitals{States: make([]types.DeviceState, 0)}
		Expect(vitals.GetVitalStates()).To(BeAssignableToTypeOf([]types.DeviceState{}))
	})

	It("UpdateStates() is actually callable at Runtime", func() {
		vitals := Vitals{States: make([]types.DeviceState, 0)}
		Expect(func() { vitals.UpdateStates() }).NotTo(Panic())
	})

})
