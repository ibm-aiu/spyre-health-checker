package server_test

import (
	"fmt"
	"net/http"
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

	Describe("HTTP Health Check Endpoints", func() {
		var httpPort int

		BeforeEach(func() {
			httpPort = 18080 // Use a different port for testing
		})

		It("should return 200 OK for /healthz endpoint", func() {
			err := TestHealthServer.StartHTTPServer(httpPort)
			Expect(err).NotTo(HaveOccurred())

			// Give the server a moment to start
			time.Sleep(100 * time.Millisecond)

			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", httpPort))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should return 200 OK for /readyz endpoint when server is ready", func() {
			// Server should already be ready from previous tests
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/readyz", httpPort))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should handle multiple concurrent health check requests", func() {
			done := make(chan bool, 10)

			for i := 0; i < 10; i++ {
				go func() {
					resp, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", httpPort))
					Expect(err).NotTo(HaveOccurred())
					defer resp.Body.Close()
					Expect(resp.StatusCode).To(Equal(http.StatusOK))
					done <- true
				}()
			}

			// Wait for all requests to complete
			for i := 0; i < 10; i++ {
				Eventually(done).Should(Receive())
			}
		})
	})
})
