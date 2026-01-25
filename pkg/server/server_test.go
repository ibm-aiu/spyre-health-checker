package server_test

import (
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	utils "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	"github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	. "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/server"
	"github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
)

var _ = Describe("Server", Ordered, func() {

	var c *Client
	BeforeAll(func() {
		c = NewClient()
		c.Start()
	})

	AfterAll(func() {
		c.Stop()
	})

	It("pseudo card health request", func() {
		Eventually(func(g Gomega) {
			healths := c.GetHealths()
			g.Expect(healths).NotTo(BeNil())
			g.Expect(healths).To(HaveLen(9))
			for pciAddr, health := range healths {
				if slices.Contains(utils.BadCards, pciAddr) {
					Expect(health).To(BeFalse())
				} else {
					Expect(slices.Contains(utils.GoodCards, pciAddr))
					Expect(slices.Contains(utils.VFCards, pciAddr))
					Expect(health).To(BeTrue())
				}
			}
		}).WithTimeout(10 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
	})

	It("can update healths", func() {
		TestHealthServer.UpdateHealths([]types.DeviceState{
			{PciAddress: "0000:1a:00.0", State: spyre.DEVICE_STATE_IN_ERROR},
		})
		Eventually(func(g Gomega) {
			healths := c.GetHealths()
			g.Expect(healths).NotTo(BeNil())
			g.Expect(healths).To(HaveLen(1))
			for pciAddr, health := range healths {
				Expect(pciAddr).To(Equal("0000:1a:00.0"))
				Expect(health).To(BeFalse())
			}
		}).WithTimeout(10 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
	})
})
