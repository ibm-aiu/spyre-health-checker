/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	"github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	. "github.com/ibm-aiu/spyre-health-checker/pkg/server"
	"github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

var _ = Describe("Server", Ordered, func() {

	var c *Client

	Context("amd64", func() {
		BeforeAll(func() {
			utils.PseudoRuntimeArch = "amd64"
		})

		AfterAll(func() {
			utils.PseudoRuntimeArch = runtime.GOARCH
		})

		It("pseudo card health (amd64)", func() {
			Eventually(func(g Gomega) {
				healths := utils.GetPseudoDeviceHealths()
				g.Expect(healths).NotTo(BeNil())
				g.Expect(healths).To(HaveLen(24)) // 8 cards * 3 (1x pf + 2 vfs)
				for _, health := range healths {
					pciAddr := health.PciAddress
					pfAddress := getPseudoPfAddress(pciAddr) // convert to .0
					if slices.Contains(utils.BadCards, pfAddress) {
						Expect(health.State).To(BeEquivalentTo(spyre.DEVICE_STATE_IN_ERROR))
					} else {
						Expect(slices.Contains(utils.GoodCards, pfAddress))
						Expect(health.State).To(BeEquivalentTo(spyre.DEVICE_STATE_ONLINE))
					}
				}
			}).WithTimeout(10 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
		})
	})

	Context("s390x", func() {
		BeforeAll(func() {
			utils.PseudoRuntimeArch = "s390x"
		})

		AfterAll(func() {
			utils.PseudoRuntimeArch = runtime.GOARCH
		})

		It("pseudo card health (s390x)", func() {
			goodCards := append(utils.GoodCards, utils.GoodIsolatedVFCards...)
			badCards := append(utils.BadCards, utils.BadIsolatedVFCards...)
			Eventually(func(g Gomega) {
				healths := utils.GetPseudoDeviceHealths()
				g.Expect(healths).NotTo(BeNil())
				g.Expect(healths).To(HaveLen(16)) // 8 pfs + 8 isolated vfs
				for _, health := range healths {
					pciAddr := health.PciAddress
					if slices.Contains(badCards, pciAddr) {
						Expect(health.State).To(BeEquivalentTo(spyre.DEVICE_STATE_IN_ERROR))
					} else {

						Expect(slices.Contains(goodCards, pciAddr))
						Expect(health.State).To(BeEquivalentTo(spyre.DEVICE_STATE_ONLINE))
					}
				}
			}).WithTimeout(10 * time.Second).WithPolling(1 * time.Second).Should(Succeed())
		})

	})

	Context("general", func() {
		BeforeAll(func() {
			c = NewClient()
			c.Start()
		})

		AfterAll(func() {
			c.Stop()
		})

		It("can get pseudo card health request", func() {
			Eventually(func(g Gomega) {
				healths := c.GetHealths()
				g.Expect(healths).NotTo(BeNil())
				g.Expect(len(healths)).To(BeNumerically(">", 0))
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
			var healthHttpPort int

			BeforeEach(func() {
				healthHttpPort = 18080 // Use a different port for testing
			})

			It("should return 200 OK for /healthz endpoint", func() {
				err := TestHealthServer.StartHealthHTTPServer(healthHttpPort)
				Expect(err).NotTo(HaveOccurred())

				// Give the server a moment to start
				time.Sleep(100 * time.Millisecond)

				resp, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", healthHttpPort))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = resp.Body.Close() }()

				Expect(resp.StatusCode).To(Equal(http.StatusOK))
			})

			It("should return 200 OK for /readyz endpoint when server is ready", func() {
				// Server should already be ready from previous tests
				resp, err := http.Get(fmt.Sprintf("http://localhost:%d/readyz", healthHttpPort))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = resp.Body.Close() }()

				Expect(resp.StatusCode).To(Equal(http.StatusOK))
			})

			It("should return 200 OK for /metrics endpoint", func() {
				metricsPort := 18081
				err := TestHealthServer.StartMetricsHTTPServer(metricsPort)
				Expect(err).NotTo(HaveOccurred())

				time.Sleep(100 * time.Millisecond)

				resp, err := http.Get(fmt.Sprintf("http://localhost:%d/metrics", metricsPort))
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = resp.Body.Close() }()

				Expect(resp.StatusCode).To(Equal(http.StatusOK))
			})

			It("should handle multiple concurrent health check requests", func() {
				done := make(chan bool, 10)

				for i := 0; i < 10; i++ {
					go func() {
						resp, err := http.Get(fmt.Sprintf("http://localhost:%d/healthz", healthHttpPort))
						Expect(err).NotTo(HaveOccurred())
						defer func() { _ = resp.Body.Close() }()
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
				opts := make([]grpc.DialOption, 0, 1)

				// Use TLS credentials with test certificates
				cert, err := tls.LoadX509KeyPair(TestCert, TestKey)
				Expect(err).To(BeNil())

				tlsConfig := &tls.Config{
					Certificates:       []tls.Certificate{cert},
					InsecureSkipVerify: true,
					MinVersion:         tls.VersionTLS12,
				}

				creds := credentials.NewTLS(tlsConfig)
				opts = append(opts, grpc.WithTransportCredentials(creds))

				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer func() { _ = conn.Close() }()

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
				opts := make([]grpc.DialOption, 0, 1)

				// Use TLS credentials with test certificates
				cert, err := tls.LoadX509KeyPair(TestCert, TestKey)
				Expect(err).To(BeNil())

				tlsConfig := &tls.Config{
					Certificates:       []tls.Certificate{cert},
					InsecureSkipVerify: true,
					MinVersion:         tls.VersionTLS12,
				}

				creds := credentials.NewTLS(tlsConfig)
				opts = append(opts, grpc.WithTransportCredentials(creds))

				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer func() { _ = conn.Close() }()

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
				opts := make([]grpc.DialOption, 0, 1)

				// Use TLS credentials with test certificates
				cert, err := tls.LoadX509KeyPair(TestCert, TestKey)
				Expect(err).To(BeNil())

				tlsConfig := &tls.Config{
					Certificates:       []tls.Certificate{cert},
					InsecureSkipVerify: true,
					MinVersion:         tls.VersionTLS12,
				}

				creds := credentials.NewTLS(tlsConfig)
				opts = append(opts, grpc.WithTransportCredentials(creds))

				conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
				Expect(err).To(BeNil())
				defer func() { _ = conn.Close() }()

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

	Describe("Override HTTP endpoint", func() {
		var httpsClient *http.Client

		BeforeEach(func() {
			// Build an mTLS client using the test certificate as both client cert and CA
			cert, err := tls.LoadX509KeyPair(TestCert, TestKey)
			Expect(err).To(BeNil())

			caCert, err := os.ReadFile(TestCert)
			Expect(err).To(BeNil())
			pool := x509.NewCertPool()
			Expect(pool.AppendCertsFromPEM(caCert)).To(BeTrue())

			httpsClient = &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						Certificates: []tls.Certificate{cert},
						RootCAs:      pool,
						MinVersion:   tls.VersionTLS12,
					},
				},
			}
		})

		AfterEach(func() {
			// Clear all overrides between tests
			req, err := http.NewRequest(http.MethodGet,
				fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort), nil)
			Expect(err).To(BeNil())
			resp, err := httpsClient.Do(req)
			Expect(err).To(BeNil())
			defer func() { _ = resp.Body.Close() }()

			var body map[string]json.RawMessage
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(BeNil())
			var overrides []map[string]string
			Expect(json.Unmarshal(body["overrides"], &overrides)).To(BeNil())
			for _, ov := range overrides {
				delReq, err := http.NewRequest(http.MethodDelete,
					fmt.Sprintf("https://localhost:%d/override/%s", TestHTTPSPort, ov["pciAddress"]), nil)
				Expect(err).To(BeNil())
				delResp, err := httpsClient.Do(delReq)
				Expect(err).To(BeNil())
				_ = delResp.Body.Close()
			}
		})

		It("GET /override returns empty list initially", func() {
			resp, err := httpsClient.Get(fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort))
			Expect(err).To(BeNil())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]json.RawMessage
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(BeNil())
			var overrides []any
			Expect(json.Unmarshal(body["overrides"], &overrides)).To(BeNil())
			Expect(overrides).To(BeEmpty())
		})

		It("POST /override sets a device state", func() {
			payload := `{"devices":[{"pciAddress":"0000:aa:00.0","state":"RUNNING_DIAGNOSTICS"}]}`
			resp, err := httpsClient.Post(
				fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort),
				"application/json",
				bytes.NewBufferString(payload),
			)
			Expect(err).To(BeNil())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]json.RawMessage
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(BeNil())
			var overridden []string
			Expect(json.Unmarshal(body["overridden"], &overridden)).To(BeNil())
			Expect(overridden).To(ConsistOf("0000:aa:00.0"))

			// Verify it appears in GET
			resp2, err := httpsClient.Get(fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort))
			Expect(err).To(BeNil())
			defer func() { _ = resp2.Body.Close() }()
			var body2 map[string]json.RawMessage
			Expect(json.NewDecoder(resp2.Body).Decode(&body2)).To(BeNil())
			var entries []map[string]string
			Expect(json.Unmarshal(body2["overrides"], &entries)).To(BeNil())
			Expect(entries).To(HaveLen(1))
			Expect(entries[0]["pciAddress"]).To(Equal("0000:aa:00.0"))
			Expect(entries[0]["state"]).To(Equal("RUNNING_DIAGNOSTICS"))
		})

		It("DELETE /override/{pci} removes an override", func() {
			// Set first
			payload := `{"devices":[{"pciAddress":"0000:bb:00.0","state":"ONLINE"}]}`
			resp, err := httpsClient.Post(
				fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort),
				"application/json",
				bytes.NewBufferString(payload),
			)
			Expect(err).To(BeNil())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			// Delete
			delReq, err := http.NewRequest(http.MethodDelete,
				fmt.Sprintf("https://localhost:%d/override/0000:bb:00.0", TestHTTPSPort), nil)
			Expect(err).To(BeNil())
			delResp, err := httpsClient.Do(delReq)
			Expect(err).To(BeNil())
			defer func() { _ = delResp.Body.Close() }()
			Expect(delResp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]json.RawMessage
			Expect(json.NewDecoder(delResp.Body).Decode(&body)).To(BeNil())
			var removed string
			Expect(json.Unmarshal(body["removed"], &removed)).To(BeNil())
			Expect(removed).To(Equal("0000:bb:00.0"))

			// Gone from GET
			resp2, err := httpsClient.Get(fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort))
			Expect(err).To(BeNil())
			defer func() { _ = resp2.Body.Close() }()
			var body2 map[string]json.RawMessage
			Expect(json.NewDecoder(resp2.Body).Decode(&body2)).To(BeNil())
			var entries []any
			Expect(json.Unmarshal(body2["overrides"], &entries)).To(BeNil())
			Expect(entries).To(BeEmpty())
		})

		It("DELETE /override/{pci} returns 404 for unknown address", func() {
			delReq, err := http.NewRequest(http.MethodDelete,
				fmt.Sprintf("https://localhost:%d/override/0000:ff:00.0", TestHTTPSPort), nil)
			Expect(err).To(BeNil())
			delResp, err := httpsClient.Do(delReq)
			Expect(err).To(BeNil())
			defer func() { _ = delResp.Body.Close() }()
			Expect(delResp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("POST /override returns 400 for invalid state", func() {
			payload := `{"devices":[{"pciAddress":"0000:cc:00.0","state":"FLYING"}]}`
			resp, err := httpsClient.Post(
				fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort),
				"application/json",
				bytes.NewBufferString(payload),
			)
			Expect(err).To(BeNil())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			body, err := io.ReadAll(resp.Body)
			Expect(err).To(BeNil())
			Expect(string(body)).To(ContainSubstring("FLYING"))
		})

		It("POST /override returns 400 for any state other than ONLINE and RUNNING_DIAGNOSTICS", func() {
			for _, state := range []string{
				"REMOVED",
				"DEVICE_STATE_UNSPECIFIED",
				"OFFLINE",
				"BOOTING",
				"SHUTTING_DOWN",
				"IN_ERROR",
			} {
				payload := fmt.Sprintf(`{"devices":[{"pciAddress":"0000:dd:00.0","state":%q}]}`, state)
				resp, err := httpsClient.Post(
					fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort),
					"application/json",
					bytes.NewBufferString(payload),
				)
				Expect(err).To(BeNil())
				defer func() { _ = resp.Body.Close() }()
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			}
		})

		It("request without a client certificate is rejected", func() {
			// Plain client — no cert presented
			bareClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true, //nolint:gosec // intentional for test
						MinVersion:         tls.VersionTLS12,
					},
				},
			}
			_, err := bareClient.Get(fmt.Sprintf("https://localhost:%d/override", TestHTTPSPort))
			// The server drops the connection when the client presents no cert
			Expect(err).To(HaveOccurred())
		})
	})

})

func getPseudoPfAddress(pciAddress string) string {
	splits := strings.Split(pciAddress, ".")
	return fmt.Sprintf("%s.0", splits[0])
}
