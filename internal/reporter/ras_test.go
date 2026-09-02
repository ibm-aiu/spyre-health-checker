/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

const (
	WorkerContainerName = "worker"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// spyrePod builds a minimal pod that requests an ibm.com/spyre_gpu resource.
func spyrePod(phase corev1.PodPhase, containerStatuses []corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: WorkerContainerName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"ibm.com/spyre_gpu": resource.MustParse("1"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase:             phase,
			ContainerStatuses: containerStatuses,
		},
	}
}

// spyrePodNamed wraps spyrePod with name, namespace and node assignment.
func spyrePodNamed(name string, phase corev1.PodPhase, statuses []corev1.ContainerStatus) *corev1.Pod {
	p := spyrePod(phase, statuses)
	p.Name = name
	p.Namespace = "default"
	p.Spec.NodeName = nodeName
	return p
}

// crashStatus returns a ContainerStatus for a container in CrashLoopBackOff.
func crashStatus(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
		},
	}
}

const (
	nodeName  = "test-node"
	senLine   = `[unspecified]  INFO 02.08.2026 09:44:46.739465 [pf_interface.cpp: 208] Reusing PfInterface forSEN:VFIO:TYPE1:0000:1a:00.0, usage = 2` // nolint: lll
	rasLine   = `ERRR 03.08.2026 22:12:26.750593 [ ras_base.hpp: 95] {"BAR":"PFBAR01","name":"RAS::PCI::PCIeFailure","severity":"ERROR"}`              // nolint: lll
	otherLine = `INFO 03.08.2026 22:12:20.000000 [ somewhere.cpp: 1] normal log line`
)

// ---------------------------------------------------------------------------
// containsRASError
// ---------------------------------------------------------------------------

var _ = Describe("containsRASError", func() {
	DescribeTable("line matching",
		func(line string, want bool) {
			Expect(containsRASError(line)).To(Equal(want))
		},
		Entry("full RAS error line matches",
			rasLine, true),
		Entry("only RAS name present → no match",
			`{"name":"RAS::PCI::PCIeFailure"}`, false),
		Entry("only ERROR severity present → no match",
			`{"severity":"ERROR"}`, false),
		Entry("neither present → no match",
			`INFO normal log line`, false),
		Entry("empty string → no match",
			``, false),
	)
})

// ---------------------------------------------------------------------------
// extractSENPCIAddress
// ---------------------------------------------------------------------------

var _ = Describe("extractSENPCIAddress", func() {
	DescribeTable("PCI address extraction",
		func(line, wantAddr string) {
			Expect(extractSENPCIAddress(line)).To(Equal(wantAddr))
		},
		Entry("full-domain address extracted",
			senLine, TestPCIAddress),
		Entry("short-form address normalised to full domain",
			`Reusing PfInterface forSEN:VFIO:TYPE1:1a:00.0, usage = 2`, TestPCIAddress),
		Entry("line without SEN pattern returns empty",
			rasLine, ""),
		Entry("empty string returns empty",
			``, ""),
	)
})

// ---------------------------------------------------------------------------
// requestsSpyreResource
// ---------------------------------------------------------------------------

var _ = Describe("requestsSpyreResource", func() {
	DescribeTable("pod resource filter",
		func(pod *corev1.Pod, want bool) {
			Expect(requestsSpyreResource(pod)).To(Equal(want))
		},
		Entry("pod with ibm.com/spyre_gpu → accepted",
			spyrePod(corev1.PodRunning, nil), true),
		Entry("pod with no ibm.com/spyre_* resource → rejected",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: WorkerContainerName, Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{"cpu": resource.MustParse("1")},
						}},
					},
				},
			}, false),
		Entry("pod with no resource requests → rejected",
			&corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: WorkerContainerName}}}}, false),
	)
})

// ---------------------------------------------------------------------------
// isFailedOrCrashLooping
// ---------------------------------------------------------------------------

var _ = Describe("isFailedOrCrashLooping", func() {
	DescribeTable("pod qualification",
		func(pod *corev1.Pod, want bool) {
			Expect(isFailedOrCrashLooping(pod)).To(Equal(want))
		},
		Entry("Failed phase → accepted",
			spyrePod(corev1.PodFailed, nil), true),
		Entry("CrashLoopBackOff container → accepted",
			spyrePod(corev1.PodRunning, []corev1.ContainerStatus{crashStatus(WorkerContainerName)}), true),
		Entry("Running phase, no crash → rejected",
			spyrePod(corev1.PodRunning, nil), false),
		Entry("Pending phase, no crash → rejected",
			spyrePod(corev1.PodPending, nil), false),
		Entry("Succeeded phase → rejected",
			spyrePod(corev1.PodSucceeded, nil), false),
	)
})

// ---------------------------------------------------------------------------
// addRASError / Collect
// ---------------------------------------------------------------------------

var _ = Describe("RASReporter.addRASError and Collect", func() {
	It("empty reporter returns no states", func() {
		r := NewRASReporter()
		states, err := r.Collect()
		Expect(err).To(BeNil())
		Expect(states).To(BeEmpty())
	})

	It("addRASError followed by Collect returns IN_ERROR with correct metadata", func() {
		r := NewRASReporter()
		r.addRASError(TestPCIAddress)

		states, err := r.Collect()
		Expect(err).To(BeNil())
		Expect(states).To(HaveLen(1))
		Expect(states[0].PciAddress).To(Equal(TestPCIAddress))
		Expect(states[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		Expect(states[0].Source).To(Equal(RASSource))
		Expect(states[0].Priority).To(Equal(types.PriorityRAS))
	})

	It("addRASError twice for the same address is idempotent", func() {
		r := NewRASReporter()
		r.addRASError(TestPCIAddress)
		r.addRASError(TestPCIAddress)

		states, err := r.Collect()
		Expect(err).To(BeNil())
		Expect(states).To(HaveLen(1))
	})

	It("addRASError for two different addresses yields two entries", func() {
		r := NewRASReporter()
		r.addRASError(TestPCIAddress)
		r.addRASError("0000:3f:00.0")

		states, err := r.Collect()
		Expect(err).To(BeNil())
		Expect(states).To(HaveLen(2))
	})
})

// ---------------------------------------------------------------------------
// scanLines (log scanning without a kube client)
// ---------------------------------------------------------------------------

// scanLinesContent resolves log content: reads a file when the string starts
// with "file:", otherwise returns the string as-is. Evaluated inside the
// table function body so Gomega's fail handler is registered.
func scanLinesContent(s string) string {
	const filePrefix = "file:"
	if strings.HasPrefix(s, filePrefix) {
		data, err := os.ReadFile(strings.TrimPrefix(s, filePrefix))
		Expect(err).To(BeNil())
		return string(data)
	}
	return s
}

var _ = DescribeTable("RASReporter.scanLines",
	func(logContent string, wantAddrs []string) {
		r := NewRASReporter()
		r.scanLines(strings.NewReader(scanLinesContent(logContent)))

		states, err := r.Collect()
		Expect(err).To(BeNil())

		gotAddrs := make([]string, 0, len(states))
		for _, s := range states {
			gotAddrs = append(gotAddrs, s.PciAddress)
		}
		Expect(gotAddrs).To(ConsistOf(wantAddrs))
	},

	Entry("SEN line then RAS error → error recorded",
		strings.Join([]string{senLine, rasLine}, "\n"),
		[]string{TestPCIAddress},
	),
	Entry("RAS error with no prior SEN line → nothing recorded",
		rasLine,
		[]string{},
	),
	Entry("SEN line then non-RAS line → nothing recorded",
		strings.Join([]string{senLine, otherLine}, "\n"),
		[]string{},
	),
	Entry("two SEN+RAS pairs → both addresses recorded",
		strings.Join([]string{
			senLine,
			rasLine,
			`Reusing PfInterface forSEN:VFIO:TYPE1:0000:3f:00.0, usage = 1`,
			rasLine,
		}, "\n"),
		[]string{TestPCIAddress, "0000:3f:00.0"},
	),
	Entry("empty log → nothing recorded",
		"",
		[]string{},
	),
	Entry("ras_timeout_log.txt — real crash log → 0000:1a:00.0 recorded",
		"file:testdata/ras_timeout_log.txt",
		[]string{TestPCIAddress},
	),
)

// ---------------------------------------------------------------------------
// Merge integration — RASReporter priority behaviour
// ---------------------------------------------------------------------------

var _ = DescribeTable("Merge with RASReporter",
	func(tc mergeTC) {
		result, err := Merge(tc.reporters)

		if tc.wantErr {
			Expect(err).NotTo(BeNil())
		} else {
			Expect(err).To(BeNil())
		}
		Expect(result).To(HaveLen(tc.wantLen))

		if tc.wantSource != "" {
			Expect(result[0].Source).To(Equal(tc.wantSource))
			Expect(result[0].State).To(Equal(tc.wantState))
		}

		if len(tc.wantByAddr) > 0 {
			byAddr := make(map[string]types.DeviceState, len(result))
			for _, s := range result {
				byAddr[s.PciAddress] = s
			}
			for addr, want := range tc.wantByAddr {
				got, ok := byAddr[addr]
				Expect(ok).To(BeTrue(), "expected PCI address %s in result", addr)
				Expect(got.Source).To(Equal(want.Source), "source mismatch for %s", addr)
				Expect(got.State).To(Equal(want.State), "state mismatch for %s", addr)
			}
		}
	},

	Entry("empty RASReporter + lspci ONLINE → ONLINE from lspci",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				NewRASReporter(),
			},
			wantLen:    1,
			wantSource: LsPCISource,
			wantState:  pb.DEVICE_STATE_ONLINE,
		},
	),
	Entry("RASReporter IN_ERROR + lspci ONLINE for same address → RAS wins",
		func() mergeTC {
			r := NewRASReporter()
			r.addRASError(TestPCIAddress)
			return mergeTC{
				reporters: []types.Reporter{
					&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
						{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
					}},
					r,
				},
				wantLen: 1,
				wantByAddr: map[string]types.DeviceState{
					TestPCIAddress: {Source: RASSource, State: pb.DEVICE_STATE_IN_ERROR},
				},
			}
		}(),
	),
	Entry("RASReporter IN_ERROR + cardmgmt IN_ERROR for same address → RAS wins (higher priority)",
		func() mergeTC {
			r := NewRASReporter()
			r.addRASError(TestPCIAddress)
			return mergeTC{
				reporters: []types.Reporter{
					&stubReporter{name: "cardmgmt", priority: types.PriorityCardmgmt, states: []types.DeviceState{
						{
							PciAddress: TestPCIAddress,
							State:      pb.DEVICE_STATE_IN_ERROR,
							Source:     "cardmgmt",
							Priority:   types.PriorityCardmgmt,
						},
					}},
					r,
				},
				wantLen: 1,
				wantByAddr: map[string]types.DeviceState{
					TestPCIAddress: {Source: RASSource, State: pb.DEVICE_STATE_IN_ERROR},
				},
			}
		}(),
	),
	Entry("RASReporter IN_ERROR for different address than lspci → both present",
		func() mergeTC {
			r := NewRASReporter()
			r.addRASError("0000:3f:00.0")
			return mergeTC{
				reporters: []types.Reporter{
					&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
						{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
					}},
					r,
				},
				wantLen: 2,
				wantByAddr: map[string]types.DeviceState{
					TestPCIAddress: {Source: LsPCISource, State: pb.DEVICE_STATE_ONLINE},
					"0000:3f:00.0": {Source: RASSource, State: pb.DEVICE_STATE_IN_ERROR},
				},
			}
		}(),
	),
)

// ---------------------------------------------------------------------------
// Pod watcher lifecycle — DescribeTable (uses fake kube client)
// ---------------------------------------------------------------------------

// watcherTC is the input/output shape for a single watchPods lifecycle entry.
type watcherTC struct {
	// setupWatch drives the FakeWatcher while watchPods is running (goroutine).
	// Only called when watchErr is nil.
	setupWatch func(fw *k8swatch.FakeWatcher)
	// watchErr, if set, is returned by the Watch reactor instead of a watcher.
	watchErr error
	// wantDisabled is the expected r.disabled value after watchPods returns.
	wantDisabled bool
	// wantAddrs are the PCI addresses expected in r.errors after watchPods.
	wantAddrs []string
}

var _ = DescribeTable("RASReporter.watchPods lifecycle",
	func(tc watcherTC) {
		fw := k8swatch.NewFake()
		client := fake.NewSimpleClientset()

		if tc.watchErr != nil {
			client.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
				return true, nil, tc.watchErr
			})
		} else {
			client.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
				return true, fw, nil
			})
		}

		r := NewRASReporter()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if tc.setupWatch != nil {
			go tc.setupWatch(fw)
		}

		r.watchPods(ctx, client, nodeName)

		Expect(r.disabled.Load()).To(Equal(tc.wantDisabled), "disabled flag")
		states, err := r.Collect()
		Expect(err).To(BeNil())
		gotAddrs := make([]string, 0, len(states))
		for _, s := range states {
			gotAddrs = append(gotAddrs, s.PciAddress)
		}
		Expect(gotAddrs).To(ConsistOf(tc.wantAddrs), "recorded PCI addresses")
	},

	// Event-type filtering
	Entry("DELETED event → nothing recorded, not disabled",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				fw.Delete(spyrePodNamed("pod-deleted", corev1.PodFailed, nil))
				time.Sleep(50 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),
	Entry("MODIFIED Running pod (no crash) → nothing recorded",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				fw.Modify(spyrePodNamed("pod-modified", corev1.PodRunning, nil))
				time.Sleep(50 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),
	Entry("ADDED pod without ibm.com/spyre_* resource → nothing recorded",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				plain := &corev1.Pod{}
				plain.Name = "plain-pod"
				plain.Namespace = "default"
				plain.Spec.NodeName = nodeName
				plain.Status.Phase = corev1.PodFailed
				fw.Add(plain)
				time.Sleep(50 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),
	Entry("ADDED Failed spyre pod → GetLogs called (fake stub has no SEN/RAS → nothing recorded)",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				fw.Add(spyrePodNamed("pod-added-failed", corev1.PodFailed, nil))
				time.Sleep(50 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),
	Entry("ADDED CrashLoopBackOff spyre pod → GetLogs called (fake stub, nothing recorded)",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				fw.Add(spyrePodNamed("pod-added-crash", corev1.PodRunning,
					[]corev1.ContainerStatus{crashStatus(WorkerContainerName)}))
				time.Sleep(50 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),

	// Watch-level permission errors
	Entry("Watch returns 403 Forbidden → disabled=true",
		watcherTC{
			watchErr:     k8serrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil),
			wantDisabled: true,
			wantAddrs:    []string{},
		},
	),
	Entry("Watch returns 401 Unauthorized → disabled=true",
		watcherTC{
			watchErr:     k8serrors.NewUnauthorized("not authorised"),
			wantDisabled: true,
			wantAddrs:    []string{},
		},
	),
	Entry("Watch returns generic error → not disabled, watchPods returns",
		watcherTC{
			watchErr:     fmt.Errorf("connection refused"),
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),

	// Context cancellation — cancel is called before watcher emits anything;
	// close the FakeWatcher so the select unblocks and sees ctx.Done.
	Entry("ctx cancelled → watchPods exits cleanly, nothing recorded",
		watcherTC{
			setupWatch: func(fw *k8swatch.FakeWatcher) {
				// Don't send events; the test's defer cancel() will fire,
				// but watchPods blocks on the select. Close the watcher so
				// the !ok branch returns immediately.
				time.Sleep(10 * time.Millisecond)
				fw.Stop()
			},
			wantDisabled: false,
			wantAddrs:    []string{},
		},
	),
)

// ---------------------------------------------------------------------------
// Start retry-loop lifecycle
// ---------------------------------------------------------------------------

var _ = DescribeTable("RASReporter.Start retry loop",
	func(
		makeWatchReactor func(calls *atomic.Int64) k8stesting.WatchReactionFunc,
		wantDisabled bool,
		wantMinCalls int,
		wantMaxCalls int,
	) {
		client := fake.NewSimpleClientset()
		var calls atomic.Int64
		client.PrependWatchReactor("pods", makeWatchReactor(&calls))

		r := NewRASReporter()
		ctx, cancel := context.WithTimeout(context.Background(), watchRetryDelay+200*time.Millisecond)
		defer cancel()

		r.Start(ctx, client, nodeName)

		if wantDisabled {
			Eventually(r.disabled.Load, 500*time.Millisecond, 10*time.Millisecond).Should(BeTrue())
			// Wait past one retry delay to confirm no further calls.
			time.Sleep(watchRetryDelay + 50*time.Millisecond)
		} else {
			<-ctx.Done()
		}

		Expect(r.disabled.Load()).To(Equal(wantDisabled), "disabled flag")
		Expect(calls.Load()).To(BeNumerically(">=", wantMinCalls), "min Watch calls")
		Expect(calls.Load()).To(BeNumerically("<=", wantMaxCalls), "max Watch calls")
	},

	Entry("Watch 403 → disabled after first call, no retries",
		func(calls *atomic.Int64) k8stesting.WatchReactionFunc {
			return func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
				calls.Add(1)
				return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
			}
		},
		true, 1, 1,
	),
	Entry("Watch 401 → disabled after first call, no retries",
		func(calls *atomic.Int64) k8stesting.WatchReactionFunc {
			return func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
				calls.Add(1)
				return true, nil, k8serrors.NewUnauthorized("not authorised")
			}
		},
		true, 1, 1,
	),
	Entry("Watch channel immediately closed → retries multiple times until ctx expires",
		func(calls *atomic.Int64) k8stesting.WatchReactionFunc {
			return func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
				calls.Add(1)
				fw := k8swatch.NewFake()
				fw.Stop()
				return true, fw, nil
			}
		},
		false, 2, 100,
	),
)

// ---------------------------------------------------------------------------
// SetAllowedNamespaces
// ---------------------------------------------------------------------------

var _ = Describe("RASReporter.SetAllowedNamespaces", func() {
	It("nil allowedNamespaces by default (watch all)", func() {
		r := NewRASReporter()
		Expect(r.allowedNamespaces).To(BeNil())
	})

	It("setting a list populates the map", func() {
		r := NewRASReporter()
		r.SetAllowedNamespaces([]string{"ns1", "ns2"})
		Expect(r.allowedNamespaces).To(HaveLen(2))
		_, ok1 := r.allowedNamespaces["ns1"]
		_, ok2 := r.allowedNamespaces["ns2"]
		Expect(ok1).To(BeTrue())
		Expect(ok2).To(BeTrue())
	})

	It("setting an empty slice resets to nil (watch all)", func() {
		r := NewRASReporter()
		r.SetAllowedNamespaces([]string{"ns1"})
		r.SetAllowedNamespaces([]string{})
		Expect(r.allowedNamespaces).To(BeNil())
	})

	It("entries are trimmed of surrounding whitespace", func() {
		r := NewRASReporter()
		r.SetAllowedNamespaces([]string{" ns1 ", "ns2"})
		_, ok := r.allowedNamespaces["ns1"]
		Expect(ok).To(BeTrue())
	})
})

// ---------------------------------------------------------------------------
// watchPods — namespace filter
// ---------------------------------------------------------------------------

var _ = DescribeTable("RASReporter.watchPods namespace filter",
	func(allowedNS []string, podNS string, wantScanned bool) {
		fw := k8swatch.NewFake()
		client := fake.NewSimpleClientset()
		client.PrependWatchReactor("pods", func(_ k8stesting.Action) (bool, k8swatch.Interface, error) {
			return true, fw, nil
		})

		r := NewRASReporter()
		r.SetAllowedNamespaces(allowedNS)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Build a failed spyre pod in the target namespace.
		pod := spyrePod(corev1.PodFailed, nil)
		pod.Name = "test-pod"
		pod.Namespace = podNS
		pod.Spec.NodeName = nodeName

		// Track whether GetLogs was called — that proves fetchAndScanLogs ran.
		var logsCalled atomic.Bool
		client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
			logsCalled.Store(true)
			return false, nil, nil // let default handling continue
		})

		go func() {
			fw.Add(pod)
			time.Sleep(50 * time.Millisecond)
			fw.Stop()
		}()

		r.watchPods(ctx, client, nodeName)

		Expect(logsCalled.Load()).To(Equal(wantScanned), "GetLogs called")
	},

	Entry("no allowlist → pod in any namespace is scanned",
		[]string{}, "other-ns", true,
	),
	Entry("allowlist set, pod namespace matches → scanned",
		[]string{"trusted"}, "trusted", true,
	),
	Entry("allowlist set, pod namespace does not match → skipped",
		[]string{"trusted"}, "untrusted", false,
	),
	Entry("allowlist with multiple namespaces, pod in second → scanned",
		[]string{"ns-a", "ns-b"}, "ns-b", true,
	),
)
