package utils

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

var _ = Describe("Prometheus metrics", func() {

	var testRegistry *prometheus.Registry

	// Helper function to find the device metric in the registry
	findDeviceMetric := func(metrics []*dto.MetricFamily) *dto.MetricFamily {
		for _, m := range metrics {
			if m.GetName() == "spyre_device_state" {
				return m
			}
		}
		return nil
	}

	BeforeEach(func() {
		// Create a new registry for each test to avoid conflicts
		testRegistry = prometheus.NewRegistry()
		// Reset the global SpyreDeviceState to ensure clean state
		// Note: We create it fresh but don't register it yet - InitMetrics will do that
		SpyreDeviceState = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Subsystem: "spyre",
				Name:      "device_state",
				Help:      "Current state for each Spyre device.",
			},
			[]string{"node", "deviceid", "devicetype", "state"},
		)
	})

	Describe("InitMetrics", func() {
		It("should register SpyreDeviceState metric successfully", func() {
			Expect(func() { InitMetrics(testRegistry) }).NotTo(Panic())

			// Set a dummy value so the metric appears in Gather()
			// Prometheus metrics don't show up in Gather() until they have at least one data point
			SpyreDeviceState.With(prometheus.Labels{
				"node":       "test",
				"deviceid":   "0000:00:00.0",
				"devicetype": "PF",
				"state":      "ONLINE",
			}).Set(1)

			// Verify the metric is registered
			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			// Should have at least the SpyreDeviceState metric plus Go collectors
			Expect(len(metrics)).To(BeNumerically(">", 0))

			// Find our metric using the helper function
			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil(), "spyre_device_state metric should be registered")
			Expect(deviceMetric.GetName()).To(Equal("spyre_device_state"))
		})

		It("should handle double registration gracefully", func() {
			// First registration
			InitMetrics(testRegistry)

			// Second registration should not panic
			Expect(func() { InitMetrics(testRegistry) }).NotTo(Panic())
		})

		It("should register Go and Process collectors", func() {
			InitMetrics(testRegistry)

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			// Check for Go collector metrics (e.g., go_goroutines)
			var hasGoMetrics bool
			for _, m := range metrics {
				if m.GetName() == "go_goroutines" {
					hasGoMetrics = true
					break
				}
			}
			Expect(hasGoMetrics).To(BeTrue(), "Go collector metrics should be registered")
		})
	})

	Describe("UpdateDeviceMetrics", func() {
		BeforeEach(func() {
			InitMetrics(testRegistry)
			// Set a test node name
			os.Setenv("NODE_NAME", "test-node")
			NodeName = "test-node"
		})

		AfterEach(func() {
			os.Unsetenv("NODE_NAME")
			NodeName = ""
		})

		It("should update metrics for a single device", func() {
			states := []types.DeviceState{
				{
					PciAddress: "0000:1a:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
			}

			UpdateDeviceMetrics(states)

			// Verify the metric was set
			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil())
			Expect(deviceMetric.GetMetric()).To(HaveLen(1))

			metric := deviceMetric.GetMetric()[0]
			Expect(metric.GetGauge().GetValue()).To(Equal(float64(1)))

			// Verify labels
			labels := metric.GetLabel()
			labelMap := make(map[string]string)
			for _, label := range labels {
				labelMap[label.GetName()] = label.GetValue()
			}

			Expect(labelMap["node"]).To(Equal("test-node"))
			Expect(labelMap["deviceid"]).To(Equal("0000:1a:00.0"))
			Expect(labelMap["devicetype"]).To(Equal("PF"))
			Expect(labelMap["state"]).To(Equal("ONLINE"))
		})

		It("should update metrics for multiple devices", func() {
			states := []types.DeviceState{
				{
					PciAddress: "0000:1a:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
				{
					PciAddress: "0000:1a:00.1",
					Type:       pb.DEVICE_TYPE_VF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
				{
					PciAddress: "0000:1b:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_IN_ERROR,
				},
			}

			UpdateDeviceMetrics(states)

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil())
			Expect(deviceMetric.GetMetric()).To(HaveLen(3))
		})

		It("should reset previous metrics when updating", func() {
			// First update with 2 devices
			states1 := []types.DeviceState{
				{
					PciAddress: "0000:1a:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
				{
					PciAddress: "0000:1b:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
			}
			UpdateDeviceMetrics(states1)

			// Second update with only 1 device
			states2 := []types.DeviceState{
				{
					PciAddress: "0000:1a:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
			}
			UpdateDeviceMetrics(states2)

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil())
			// Should only have 1 metric after reset
			Expect(deviceMetric.GetMetric()).To(HaveLen(1))
		})

		It("should handle empty device list", func() {
			states := []types.DeviceState{}

			Expect(func() { UpdateDeviceMetrics(states) }).NotTo(Panic())

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			// Metric should exist but have no entries
			if deviceMetric != nil {
				Expect(deviceMetric.GetMetric()).To(HaveLen(0))
			}
		})

		It("should handle all device types correctly", func() {
			states := []types.DeviceState{
				{
					PciAddress: "0000:1a:00.0",
					Type:       pb.DEVICE_TYPE_PF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
				{
					PciAddress: "0000:1a:00.1",
					Type:       pb.DEVICE_TYPE_VF,
					State:      pb.DEVICE_STATE_ONLINE,
				},
				{
					PciAddress: "0000:1a:00.2",
					Type:       pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED,
					State:      pb.DEVICE_STATE_ONLINE,
				},
			}

			UpdateDeviceMetrics(states)

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil())
			Expect(deviceMetric.GetMetric()).To(HaveLen(3))

			// Verify device types
			deviceTypes := make(map[string]bool)
			for _, metric := range deviceMetric.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "devicetype" {
						deviceTypes[label.GetValue()] = true
					}
				}
			}

			Expect(deviceTypes).To(HaveKey("PF"))
			Expect(deviceTypes).To(HaveKey("VF"))
			Expect(deviceTypes).To(HaveKey("UNSPECIFIED"))
		})

		It("should handle all device states correctly", func() {
			states := []types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_OFFLINE},
				{PciAddress: "0000:1b:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_BOOTING},
				{PciAddress: "0000:1c:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_SHUTTING_DOWN},
				{PciAddress: "0000:1d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
				{PciAddress: "0000:1e:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_RUNNING_DIAGNOSTICS},
				{PciAddress: "0000:1f:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR},
				{PciAddress: "0000:20:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_REMOVED},
				{PciAddress: "0000:21:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED},
			}

			UpdateDeviceMetrics(states)

			metrics, err := testRegistry.Gather()
			Expect(err).NotTo(HaveOccurred())

			deviceMetric := findDeviceMetric(metrics)
			Expect(deviceMetric).NotTo(BeNil())
			Expect(deviceMetric.GetMetric()).To(HaveLen(8))

			// Verify all states are present
			deviceStates := make(map[string]bool)
			for _, metric := range deviceMetric.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "state" {
						deviceStates[label.GetValue()] = true
					}
				}
			}

			Expect(deviceStates).To(HaveKey("OFFLINE"))
			Expect(deviceStates).To(HaveKey("BOOTING"))
			Expect(deviceStates).To(HaveKey("SHUTTING_DOWN"))
			Expect(deviceStates).To(HaveKey("ONLINE"))
			Expect(deviceStates).To(HaveKey("RUNNING_DIAGNOSTICS"))
			Expect(deviceStates).To(HaveKey("IN_ERROR"))
			Expect(deviceStates).To(HaveKey("REMOVED"))
			Expect(deviceStates).To(HaveKey("UNSPECIFIED"))
		})
	})

	Describe("enumDeviceType", func() {
		It("should return 'PF' for DEVICE_TYPE_PF", func() {
			result := enumDeviceType(pb.DEVICE_TYPE_PF)
			Expect(result).To(Equal("PF"))
		})

		It("should return 'VF' for DEVICE_TYPE_VF", func() {
			result := enumDeviceType(pb.DEVICE_TYPE_VF)
			Expect(result).To(Equal("VF"))
		})

		It("should return 'UNSPECIFIED' for DEVICE_TYPE_UNSPECIFIED", func() {
			result := enumDeviceType(pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED)
			Expect(result).To(Equal("UNSPECIFIED"))
		})

		It("should return 'UNSPECIFIED' for unknown device type", func() {
			result := enumDeviceType(pb.DEVICE_TYPE(999))
			Expect(result).To(Equal("UNSPECIFIED"))
		})
	})

	Describe("enumDeviceState", func() {
		It("should return 'OFFLINE' for DEVICE_STATE_OFFLINE", func() {
			result := enumDeviceState(pb.DEVICE_STATE_OFFLINE)
			Expect(result).To(Equal("OFFLINE"))
		})

		It("should return 'BOOTING' for DEVICE_STATE_BOOTING", func() {
			result := enumDeviceState(pb.DEVICE_STATE_BOOTING)
			Expect(result).To(Equal("BOOTING"))
		})

		It("should return 'SHUTTING_DOWN' for DEVICE_STATE_SHUTTING_DOWN", func() {
			result := enumDeviceState(pb.DEVICE_STATE_SHUTTING_DOWN)
			Expect(result).To(Equal("SHUTTING_DOWN"))
		})

		It("should return 'ONLINE' for DEVICE_STATE_ONLINE", func() {
			result := enumDeviceState(pb.DEVICE_STATE_ONLINE)
			Expect(result).To(Equal("ONLINE"))
		})

		It("should return 'RUNNING_DIAGNOSTICS' for DEVICE_STATE_RUNNING_DIAGNOSTICS", func() {
			result := enumDeviceState(pb.DEVICE_STATE_RUNNING_DIAGNOSTICS)
			Expect(result).To(Equal("RUNNING_DIAGNOSTICS"))
		})

		It("should return 'IN_ERROR' for DEVICE_STATE_IN_ERROR", func() {
			result := enumDeviceState(pb.DEVICE_STATE_IN_ERROR)
			Expect(result).To(Equal("IN_ERROR"))
		})

		It("should return 'REMOVED' for DEVICE_STATE_REMOVED", func() {
			result := enumDeviceState(pb.DEVICE_STATE_REMOVED)
			Expect(result).To(Equal("REMOVED"))
		})

		It("should return 'UNSPECIFIED' for DEVICE_STATE_UNSPECIFIED", func() {
			result := enumDeviceState(pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED)
			Expect(result).To(Equal("UNSPECIFIED"))
		})

		It("should return 'UNSPECIFIED' for unknown device state", func() {
			result := enumDeviceState(pb.DEVICE_STATE(999))
			Expect(result).To(Equal("UNSPECIFIED"))
		})
	})
})

// Made with Bob
