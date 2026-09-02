/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"bufio"
	"context"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	"github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"go.uber.org/zap"
)

// senPCIRe matches the SEN:VFIO:TYPE1:<pciAddress> pattern that appears in
// workload pod logs before any RAS error, establishing which physical device
// the pod is using.
var senPCIRe = regexp.MustCompile(
	`SEN:VFIO:TYPE1:((?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])`,
)

// spyreResourcePrefix is the resource name prefix that all IBM Spyre device
// plugin resources share.
const spyreResourcePrefix = "ibm.com/spyre_"

// logTailLines is the maximum number of lines fetched from the tail of each
// container log. Capping at 100 lines keeps memory use bounded; RAS errors
// and the preceding SEN:VFIO:TYPE1 lines always appear near the end of the
// log of a crashing container.
const logTailLines int64 = 100

// watchRetryDelay is the back-off between watch re-establishment attempts.
const watchRetryDelay = 5 * time.Second

// RASReporter is a types.Reporter that carries the highest priority. It owns
// both the in-memory error map and the pod-watch goroutine that populates it.
//
// Start must be called once from main() to begin watching. The goroutine exits
// when the provided context is cancelled (SIGTERM / SIGINT). If Start is never
// called (e.g. not running inside a Kubernetes cluster) Collect() returns an
// empty slice with no error.
//
// If the Kubernetes API returns a permission error (403 Forbidden or 401
// Unauthorized) on a Watch or GetLogs call, the reporter logs a warning and
// disables itself permanently — the retry loop stops and Collect() continues
// to return whatever errors were accumulated before the permission failure.
type RASReporter struct {
	mu                sync.RWMutex
	errors            map[string]types.DeviceState // keyed by PCI address
	disabled          atomic.Bool                  // set on permission error; stops the retry loop
	log               *zap.SugaredLogger
	allowedNamespaces map[string]struct{} // nil = watch all namespaces
}

// NewRASReporter creates an empty RASReporter with a no-op logger.
// Call SetLogger to attach a real logger before Start.
func NewRASReporter() *RASReporter {
	return &RASReporter{
		errors: make(map[string]types.DeviceState),
		log:    zap.NewNop().Sugar(),
	}
}

// SetAllowedNamespaces restricts the RAS watcher to the given namespaces.
// An empty slice means "watch all namespaces" (the default).
// Must be called before Start.
func (r *RASReporter) SetAllowedNamespaces(namespaces []string) {
	if len(namespaces) == 0 {
		r.allowedNamespaces = nil
		return
	}
	m := make(map[string]struct{}, len(namespaces))
	for _, ns := range namespaces {
		m[strings.TrimSpace(ns)] = struct{}{}
	}
	r.allowedNamespaces = m
}

// SetLogger attaches a logger to the reporter. Must be called before Start.
func (r *RASReporter) SetLogger(l *zap.SugaredLogger) {
	if l == nil {
		r.log = zap.NewNop().Sugar()
		return
	}
	r.log = l
}

func (r *RASReporter) Name() string  { return "ras" }
func (r *RASReporter) Priority() int { return types.PriorityRAS }

// Collect returns a snapshot of all currently known RAS-error states, each
// stamped with Source="ras" and Priority=PriorityRAS.
func (r *RASReporter) Collect() ([]types.DeviceState, error) {
	r.mu.RLock()
	states := make([]types.DeviceState, 0, len(r.errors))
	for _, s := range r.errors {
		states = append(states, s)
	}
	r.mu.RUnlock()
	stamp(states, r.Name(), r.Priority())
	return states, nil
}

// Start launches the pod-watch goroutine and returns immediately. The goroutine
// exits when ctx is cancelled or a permission error disables the reporter.
// It must be called at most once.
func (r *RASReporter) Start(ctx context.Context, client kubernetes.Interface, nodeName string) {
	go func() {
		for {
			r.watchPods(ctx, client, nodeName)
			if r.disabled.Load() {
				return
			}
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(watchRetryDelay):
				// re-establish the watch after a brief back-off
			}
		}
	}()
}

// addRASError records pciAddress as IN_ERROR. Thread-safe; called only by the
// internal watch loop.
func (r *RASReporter) addRASError(pciAddress string) {
	r.mu.Lock()
	r.errors[pciAddress] = types.DeviceState{
		PciAddress: pciAddress,
		Type:       pb.DEVICE_TYPE_PF,
		State:      pb.DEVICE_STATE_IN_ERROR,
	}
	r.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Pod watch loop
// ---------------------------------------------------------------------------

// watchPods performs a single list/watch cycle for pods on nodeName. It returns
// when the watch channel is closed, ctx is cancelled, or an error occurs.
// On a permission error it disables the reporter and returns.
func (r *RASReporter) watchPods(ctx context.Context, client kubernetes.Interface, nodeName string) {
	fieldSel := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	watcher, err := client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{
		FieldSelector: fieldSel,
	})
	if err != nil {
		if k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
			r.log.Warnf("RAS pod watcher disabled: no permission to watch pods (%v)", err)
			r.disabled.Store(true)
		}
		return
	}
	r.log.Infof("RAS pod watcher started, watching pods on node %q", nodeName)
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}
			if event.Type != watch.Added && event.Type != watch.Modified {
				continue
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok || r.skipPod(pod) {
				continue
			}
			r.log.Infof("RAS pod watcher: failed pod detected %s/%s (phase=%s)", pod.Namespace, pod.Name, pod.Status.Phase)
			r.fetchAndScanLogs(ctx, client, pod)
		}
	}
}

func (r *RASReporter) skipPod(pod *corev1.Pod) bool {
	if !requestsSpyreResource(pod) || !isFailedOrCrashLooping(pod) {
		return true
	}
	if r.allowedNamespaces != nil {
		if _, ok := r.allowedNamespaces[pod.Namespace]; !ok {
			return true
		}
	}
	return false
}

// requestsSpyreResource returns true when the pod requests at least one
// resource whose name has the prefix "ibm.com/spyre_".
func requestsSpyreResource(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		for name := range c.Resources.Requests {
			if strings.HasPrefix(string(name), spyreResourcePrefix) {
				return true
			}
		}
	}
	return false
}

// isFailedOrCrashLooping returns true when the pod is in Failed phase or has
// at least one container waiting in CrashLoopBackOff.
func isFailedOrCrashLooping(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodFailed {
		return true
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Log scanning
// ---------------------------------------------------------------------------

// fetchAndScanLogs scans the logs of every container in pod that is either
// terminated (pod phase Failed) or crash-looping (CrashLoopBackOff).
//
// For CrashLoopBackOff containers the container is currently waiting — the
// RAS error lines are in the previous (crashed) container's log, so
// PodLogOptions.Previous is set to true for those containers.
// For Failed-phase pods the container has terminated and its log is the
// current one (Previous: false).
func (r *RASReporter) fetchAndScanLogs(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) {
	// Build a lookup from container name → whether it is crash-looping.
	crashLooping := make(map[string]bool, len(pod.Status.ContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			crashLooping[cs.Name] = true
		}
	}

	for _, c := range pod.Spec.Containers {
		isCrash := crashLooping[c.Name]
		isFailed := pod.Status.Phase == corev1.PodFailed

		// Only scan containers that have actually crashed or are crash-looping.
		if !isCrash && !isFailed {
			continue
		}

		tail := logTailLines
		opts := &corev1.PodLogOptions{
			Container: c.Name,
			// CrashLoopBackOff: container is waiting; the log we need is
			// from the previous (crashed) run.
			// Failed phase: container is terminated; current log is correct.
			Previous:  isCrash,
			TailLines: &tail,
		}
		r.scanContainerLogs(ctx, client, pod, opts)
	}
}

// scanContainerLogs streams one container's log and calls addRASError for each
// RAS error line that is preceded by a SEN:VFIO:TYPE1 address line.
// On a permission error it logs a warning and skips the container.
func (r *RASReporter) scanContainerLogs(ctx context.Context,
	client kubernetes.Interface, pod *corev1.Pod, opts *corev1.PodLogOptions) {
	stream, err := client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts).Stream(ctx)
	if err != nil {
		if k8serrors.IsForbidden(err) || k8serrors.IsUnauthorized(err) {
			r.log.Warnf("RAS pod watcher disabled: no permission to get logs for pod %s/%s (%v)", pod.Namespace, pod.Name, err)
			r.disabled.Store(true)
		}
		return
	}
	defer stream.Close() //nolint:errcheck
	r.scanLines(stream)
}

// scanLines reads log lines from r and calls addRASError whenever a RAS error
// line is preceded by a SEN:VFIO:TYPE1 address line. Separated from the kube
// call so tests can supply a plain io.Reader without a fake kube client.
func (r *RASReporter) scanLines(rd io.Reader) {
	var lastPCI string
	scanner := bufio.NewScanner(rd)
	for scanner.Scan() {
		line := scanner.Text()
		if pci := extractSENPCIAddress(line); pci != "" {
			lastPCI = pci
		}
		if containsRASError(line) && lastPCI != "" {
			r.log.Infof("RAS pod watcher: RAS error event detected for PCI address %s", lastPCI)
			r.addRASError(lastPCI)
		}
	}
}

// containsRASError returns true when line contains both the RAS event name
// marker and the ERROR severity marker.
func containsRASError(line string) bool {
	return strings.Contains(line, `"name":"RAS::`) && strings.Contains(line, `"severity":"ERROR"`)
}

// extractSENPCIAddress scans line for the SEN:VFIO:TYPE1:<pciAddress> pattern
// and returns the PCI address, or "" if not found.
//
// Example matching line:
//
//	[unspecified]  INFO ... Reusing PfInterface forSEN:VFIO:TYPE1:0000:2e:00.0, usage = 2
func extractSENPCIAddress(line string) string {
	m := senPCIRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	// Normalise short-form addresses (e.g. "2e:00.0") to "0000:2e:00.0".
	addr := m[1]
	if len(addr) == 7 {
		addr = "0000:" + addr
	}
	return addr
}
