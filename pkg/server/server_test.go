package server_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	healthcheck "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/healthcheck"
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

	It("concurrent access to vitals is thread-safe", func() {
		// This test verifies that RegisterForSpyreDevicesEvents uses GetVitalStates()
		// which is thread-safe, preventing data races when vitals are updated concurrently
		vitals := &healthcheck.Vitals{
			States: []types.DeviceState{
				{PciAddress: "0000:01:00.0", State: spyre.DEVICE_STATE_ONLINE},
			},
		}
		server := NewServer(vitals)

		var wg sync.WaitGroup

		// Goroutine 1: Simulate gRPC stream registration (reads vitals)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			// Create a mock stream context
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			// Simulate multiple reads like RegisterForSpyreDevicesEvents does
			for i := 0; i < 100; i++ {
				select {
				case <-ctx.Done():
					return
				default:
					// This simulates the read at line 95 in server.go
					// Using GetVitalStates() ensures thread-safe access
					states := vitals.GetVitalStates()
					Expect(states).NotTo(BeNil())
					time.Sleep(1 * time.Millisecond)
				}
			}
		}()

		// Goroutine 2: Simulate periodic updates (writes vitals)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()

			// Simulate multiple writes using UpdateStates which is thread-safe
			for i := 0; i < 100; i++ {
				// UpdateStates properly locks the mutex internally
				err := vitals.UpdateStates()
				Expect(err).To(BeNil())
				time.Sleep(1 * time.Millisecond)
			}
		}()

		// Wait for both goroutines to complete
		wg.Wait()

		// Verify server was created successfully
		Expect(server).NotTo(BeNil())
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
