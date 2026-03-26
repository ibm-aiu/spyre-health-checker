package healthcheck

import (
	_ "embed"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
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
	VFCards = []string{
		"0000:1d:00.0",
	}
)

var _ = Describe("Parser", func() {
	It("parseLSPCI() identifies supported cards, online/error state, and device type", func() {
		states := parseLSPCI(sampleLSPCI)
		Expect(states).To(HaveLen(14))
		for _, state := range states {
			Expect(slices.Contains(unsupportedCards, state.PciAddress)).To(BeFalse())
			// Verify error state vs online state
			switch {
			case slices.Contains(errorCards, state.PciAddress):
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_IN_ERROR))
			default:
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_ONLINE))
			}
			// Verify type PF vs VF
			switch {
			case slices.Contains(VFCards, state.PciAddress):
				Expect(state.Type).To(BeEquivalentTo(pb.DEVICE_TYPE_VF))
			default:
				Expect(state.Type).To(BeEquivalentTo(pb.DEVICE_TYPE_PF))
			}
		}
	})
})
