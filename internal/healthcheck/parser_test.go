package healthcheck

import (
	_ "embed"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
)

//go:embed assets/input.txt
var sampleLSPCI string

var (
	errorCards = []string{
		"0000:1b:00.0",
	}
	unsupportedCards = []string{
		"0000:1a:00.0",
		"0000:1c:00.0",
	}
)

var _ = Describe("Parser", func() {
	It("parseLSPCI", func() {
		states := parseLSPCI(sampleLSPCI)
		Expect(states).To(HaveLen(14))
		for _, state := range states {
			Expect(slices.Contains(unsupportedCards, state.PciAddress)).To(BeFalse())
			switch {
			case slices.Contains(errorCards, state.PciAddress):
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_IN_ERROR))
			default:
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_ONLINE))
			}
		}
	})
})
