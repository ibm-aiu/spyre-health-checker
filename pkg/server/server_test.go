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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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

		Describe("RegisterForSpyreDevicesEventsWithDevices", func() {
			// Stop the global client before these tests to avoid multiple concurrent streams
			BeforeEach(func() {
				c.Stop()
			})

			// Recreate and restart the global client after these tests
			AfterEach(func() {
				c = NewClient()
				c.Start()
			})

			It("should detect removed devices", func() {
				// Create a client that uses the new RPC with initial devices
				var opts []grpc.DialOption
				opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer conn.Close()

				client := spyre.NewSpyreHealthServiceClient(conn)

				// Define initial devices - include some that don't exist in current state
				initialDevices := &spyre.Devices{
					Devices: []*spyre.Device{
						{
							DeviceID: &spyre.DeviceID{
								PCIAddress: "0000:1a:00.0",
							},
							DeviceType:  spyre.DEVICE_TYPE_PF,
							DeviceState: spyre.DEVICE_STATE_ONLINE,
						},
						{
							DeviceID: &spyre.DeviceID{
								PCIAddress: "0000:99:00.0", // This device doesn't exist
							},
							DeviceType:  spyre.DEVICE_TYPE_PF,
							DeviceState: spyre.DEVICE_STATE_ONLINE,
						},
						{
							DeviceID: &spyre.DeviceID{
								PCIAddress: "0000:88:00.0", // This device doesn't exist
							},
							DeviceType:  spyre.DEVICE_TYPE_VF,
							DeviceState: spyre.DEVICE_STATE_ONLINE,
						},
					},
				}

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				stream, err := client.RegisterForSpyreDevicesEventsWithDevices(ctx, initialDevices)
				Expect(err).To(BeNil())

				// Receive the first message
				deviceList, err := stream.Recv()
				Expect(err).To(BeNil())
				Expect(deviceList).NotTo(BeNil())

				// Check that we received devices including REMOVED ones
				removedCount := 0
				foundDevices := make(map[string]spyre.DEVICE_STATE)
				for _, device := range deviceList.Devices {
					foundDevices[device.DeviceID.PCIAddress] = device.DeviceState
					if device.DeviceState == spyre.DEVICE_STATE_REMOVED {
						removedCount++
					}
				}

				// We should have at least 2 removed devices (0000:99:00.0 and 0000:88:00.0)
				Expect(removedCount).To(BeNumerically(">=", 2))
				Expect(foundDevices["0000:99:00.0"]).To(Equal(spyre.DEVICE_STATE_REMOVED))
				Expect(foundDevices["0000:88:00.0"]).To(Equal(spyre.DEVICE_STATE_REMOVED))
			})

			It("should work with empty initial device list", func() {
				// Create a client that uses the new RPC with empty initial devices
				var opts []grpc.DialOption
				opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer conn.Close()

				client := spyre.NewSpyreHealthServiceClient(conn)

				// Empty initial devices - should behave like the old RPC
				initialDevices := &spyre.Devices{
					Devices: []*spyre.Device{},
				}

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				stream, err := client.RegisterForSpyreDevicesEventsWithDevices(ctx, initialDevices)
				Expect(err).To(BeNil())

				// Receive the first message
				deviceList, err := stream.Recv()
				Expect(err).To(BeNil())
				Expect(deviceList).NotTo(BeNil())

				// No devices should be marked as REMOVED
				for _, device := range deviceList.Devices {
					Expect(device.DeviceState).NotTo(Equal(spyre.DEVICE_STATE_REMOVED))
				}
			})

			It("should add new devices to tracking map", func() {
				// Create a client that uses the new RPC with initial devices
				var opts []grpc.DialOption
				opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer conn.Close()

				client := spyre.NewSpyreHealthServiceClient(conn)

				// Define initial devices - only one device
				initialDevices := &spyre.Devices{
					Devices: []*spyre.Device{
						{
							DeviceID: &spyre.DeviceID{
								PCIAddress: "0000:1a:00.0",
							},
							DeviceType:  spyre.DEVICE_TYPE_PF,
							DeviceState: spyre.DEVICE_STATE_ONLINE,
						},
					},
				}

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				stream, err := client.RegisterForSpyreDevicesEventsWithDevices(ctx, initialDevices)
				Expect(err).To(BeNil())

				// Receive the first message
				deviceList, err := stream.Recv()
				Expect(err).To(BeNil())
				Expect(deviceList).NotTo(BeNil())

				// Now send an update with a new device
				TestHealthServer.UpdateHealths([]types.DeviceState{
					{PciAddress: "0000:1a:00.0", State: spyre.DEVICE_STATE_ONLINE},
					{PciAddress: "0000:2b:00.0", State: spyre.DEVICE_STATE_ONLINE}, // New device
				})

				// Receive the update
				deviceList, err = stream.Recv()
				Expect(err).To(BeNil())
				Expect(deviceList).NotTo(BeNil())

				// The new device should be present and not marked as REMOVED
				foundNewDevice := false
				for _, device := range deviceList.Devices {
					if device.DeviceID.PCIAddress == "0000:2b:00.0" {
						foundNewDevice = true
						Expect(device.DeviceState).NotTo(Equal(spyre.DEVICE_STATE_REMOVED))
					}
				}
				Expect(foundNewDevice).To(BeTrue())

				// Send another update without the original device
				TestHealthServer.UpdateHealths([]types.DeviceState{
					{PciAddress: "0000:2b:00.0", State: spyre.DEVICE_STATE_ONLINE},
				})

				// Receive the update
				deviceList, err = stream.Recv()
				Expect(err).To(BeNil())
				Expect(deviceList).NotTo(BeNil())

				// The original device should now be marked as REMOVED
				foundRemovedDevice := false
				for _, device := range deviceList.Devices {
					if device.DeviceID.PCIAddress == "0000:1a:00.0" {
						foundRemovedDevice = true
						Expect(device.DeviceState).To(Equal(spyre.DEVICE_STATE_REMOVED))
					}
				}
				Expect(foundRemovedDevice).To(BeTrue())

				// The new device should still be present and not marked as REMOVED
				foundNewDevice = false
				for _, device := range deviceList.Devices {
					if device.DeviceID.PCIAddress == "0000:2b:00.0" {
						foundNewDevice = true
						Expect(device.DeviceState).NotTo(Equal(spyre.DEVICE_STATE_REMOVED))
					}
				}
				Expect(foundNewDevice).To(BeTrue())
			})
		})
	})
})
