# RAS Watcher Reporter

**Milestone:** 1.4.0

## Overview

Introduce `RASReporter` (`internal/reporter/ras.go`), a new highest-priority reporter
that watches Kubernetes pods on the local node, detects failures in workloads using IBM
Spyre devices, and injects `DEVICE_STATE_IN_ERROR` entries into the existing
`reporter.Merge()` pipeline.

The reporter owns both the in-memory error map **and** the background goroutine that
populates it. `main()` calls `rasReporter.Start(ctx, kubeClient, nodeName)` once; the
goroutine exits cleanly when the context is cancelled on `SIGTERM`/`SIGINT`.

---

## Motivation

RAS (Reliability, Availability, Serviceability) hardware errors are surfaced in workload
pod logs, not in kernel sysfs or any service polled by the existing reporters. When a
workload pod crashes due to a PCIe failure on a Spyre device, the existing `LSPCIReporter`
and `CardmgmtReporter` may still report the device as `ONLINE` — because the hardware
registers are intact but the device is functionally broken for that workload.

`RASReporter` closes this gap by:

- Watching for pods in `Failed` phase or `CrashLoopBackOff` that request an
  `ibm.com/spyre_*` resource.
- Scanning their logs for structured RAS error lines.
- Recording `DEVICE_STATE_IN_ERROR` with priority 10 — high enough to override any
  `ONLINE` assertion from lower-priority reporters.

---

## Key Concepts

### Pod qualification

A pod is scanned if and only if **both** conditions are met:

| Condition | Check |
|---|---|
| Requests an IBM Spyre device | At least one container requests a resource with prefix `ibm.com/spyre_` |
| Is failed or crash-looping | Pod phase is `Failed` **or** at least one container status is `CrashLoopBackOff` |

### Log scanning algorithm

Container logs are scanned line-by-line using a stateful two-marker approach:

1. **SEN line** — contains `SEN:VFIO:TYPE1:<pciAddress>`. When matched, `lastPCI` is
   updated to the extracted PCI address.
2. **RAS error line** — contains both `"name":"RAS::` and `"severity":"ERROR"`. When
   matched and `lastPCI != ""`, `addRASError(lastPCI)` is called.

The SEN line always appears before the corresponding RAS error in the same log stream,
establishing which physical device the pod was using at the time of failure.

Example log lines (confirmed format):

```
[unspecified]  INFO 02.08.2026 09:44:46.739465 [pf_interface.cpp: 208] Reusing PfInterface forSEN:VFIO:TYPE1:0000:2e:00.0, usage = 2
ERRR 03.08.2026 22:12:26.750593 [ ras_base.hpp: 95] {"BAR":"PFBAR01","name":"RAS::PCI::PCIeFailure","severity":"ERROR",...}
```

The `"BAR"` field in the RAS line is a logical name, not a PCI address. The physical
PCI address is tracked via `lastPCI` from the preceding SEN line.

### PCI address normalisation

`extractSENPCIAddress` normalises short-form addresses to the full four-tuple form:

| Input (from log) | Returned |
|---|---|
| `0000:2e:00.0` | `0000:2e:00.0` (unchanged) |
| `2e:00.0` | `0000:2e:00.0` (domain prepended) |

Regexp used: `SEN:VFIO:TYPE1:((?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])`

### In-memory state, reset on restart

The error map is held entirely in process memory. If the health-checker pod is
restarted the map starts empty and is repopulated only if the failing workload pod is
still present and its logs still contain RAS error lines when the watcher re-scans it.
There is no persistence, no admin clear endpoint, and no cross-restart state.

### Permission errors → permanent disable

If the Kubernetes API returns `403 Forbidden` or `401 Unauthorized` on a `Watch` or
`GetLogs` call, the reporter logs a warning, sets `disabled = true`, and the retry loop
exits permanently. `Collect()` continues to return whatever errors were accumulated
before the permission failure. No further API calls are made.

For any other watch error (e.g. transient network failure), the loop backs off by
`watchRetryDelay` (5 s) and retries.

---

## API

### `RASReporter` struct

```go
type RASReporter struct {
    mu       sync.RWMutex
    errors   map[string]types.DeviceState // keyed by PCI address
    disabled atomic.Bool                  // set on permission error; stops the retry loop
    log      *zap.SugaredLogger
}
```

### Constructor and logger

```go
func NewRASReporter() *RASReporter
func (r *RASReporter) SetLogger(l *zap.SugaredLogger)
```

`NewRASReporter` returns a reporter with an empty error map and a no-op logger.
`SetLogger` must be called before `Start` if structured log output is desired.

### `types.Reporter` interface

```go
func (r *RASReporter) Name()     string                        { return "ras" }
func (r *RASReporter) Priority() int                           { return types.PriorityRAS }
func (r *RASReporter) Collect()  ([]types.DeviceState, error)
```

`Collect` acquires a read lock, snapshots the error map into a slice, calls `stamp()`
to set `Source="ras"` and `Priority=PriorityRAS` on each entry, and returns. It never
returns an error.

### `Start`

```go
func (r *RASReporter) Start(ctx context.Context, client kubernetes.Interface, nodeName string)
```

Launches the pod-watch goroutine and returns immediately. The goroutine runs the
following retry loop:

```
loop:
  watchPods(ctx, client, nodeName)   // blocks until watch channel closed or ctx done
  if disabled → return
  select:
    ctx.Done() → return
    time.After(5s) → continue loop   // back-off before re-establishing watch
```

`Start` must be called at most once. Calling it without a Kubernetes client (e.g. in
development) is safe; `Collect()` returns empty.

---

## Priority

Defined in [`pkg/types/types.go`](../pkg/types/types.go):

| Constant | Value | Reporter |
|---|---|---|
| `PriorityLSPCI` | 1 | `LSPCIReporter` — hardware scan (lowest) |
| `PriorityCardmgmt` | 5 | `CardmgmtReporter` — card management service |
| `PriorityRAS` | 10 | `RASReporter` — RAS pod watcher (highest) |

`PriorityRAS = 10` ensures that a RAS-detected hardware error always outranks any
`ONLINE` assertion from lspci (1) or cardmgmt (5). The sticky-error rule in `Merge`
then prevents a lower-priority `ONLINE` from displacing it:

| Existing (RAS) | Incoming (lspci/cardmgmt) | Result |
|---|---|---|
| `IN_ERROR` (p=10) | `ONLINE` (p=1 or 5) | **`IN_ERROR` kept** — lower priority cannot clear |
| `IN_ERROR` (p=10) | `IN_ERROR` (p=1 or 5) | **`IN_ERROR` kept** — lower priority loses |
| _(absent)_ | `ONLINE` (p=1) | **`ONLINE`** — RAS map is empty |

---

## `main.go` wiring

```go
// Signal-aware context — cancelled on SIGTERM/SIGINT.
// defer cancel() is registered AFTER defer s.Stop() so it fires FIRST (LIFO),
// ensuring the watcher goroutine exits before the server closes its update queue.
defer s.Stop()
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer cancel()

rasReporter := reporter.NewRASReporter()
rasReporter.SetLogger(logger)
reporters := buildReporters(*enabledReporters, rasReporter) // always appended

if cfg, err := rest.InClusterConfig(); err != nil {
    logger.Warnf("RAS pod watcher disabled: not running in-cluster (%v)", err)
} else if kubeClient, err := kubernetes.NewForConfig(cfg); err != nil {
    logger.Warnf("RAS pod watcher disabled: failed to build kube client (%v)", err)
} else {
    rasReporter.Start(ctx, kubeClient, utils.NodeName)
}
```

Failure to build the kube client is non-fatal — the health-checker continues to
function; `RASReporter.Collect()` returns empty with no error.

---

## File Layout

```
internal/reporter/
├── ras.go               # RASReporter, Start, watchPods, fetchAndScanLogs,
│                        # scanLines, scanContainerLogs, addRASError,
│                        # containsRASError, extractSENPCIAddress,
│                        # requestsSpyreResource, isFailedOrCrashLooping
└── ras_test.go          # full white-box test suite (Ginkgo v2 / Gomega)
    testdata/
    └── ras_timeout_log.txt  # real crash log used as a scanLines fixture

pkg/types/types.go       # PriorityRAS = 10 added
cmd/health-checker/main.go  # wired: NewRASReporter, Start, signal context
```

---

## Design Decisions

| Decision | Rationale |
|---|---|
| Pod watcher lives inside `RASReporter` | Single cohesive unit — the reporter owns both the error map and the goroutine that populates it; no separate package needed |
| `ctx` cancellation as the shutdown signal | `main` passes a `signal.NotifyContext` context; on `SIGTERM`/`SIGINT` the context is cancelled and the watch loop exits before `s.Stop()` is called |
| `defer cancel()` registered after `defer s.Stop()` | LIFO defers ensure context cancellation fires first, giving the watch goroutine a chance to exit before the server closes the update queue |
| `PriorityRAS = 10` (highest) | RAS is a direct hardware signal from the workload's perspective; it must outrank card management (5) and lspci (1) |
| In-memory map, reset on restart | No persistence layer needed; a restart re-scans surviving failed pods and repopulates only live errors |
| `Start` is a no-op if kube client unavailable | Health-checker must still work outside Kubernetes (dev, CI); `Collect()` returns empty with no error |
| `Previous: true` for CrashLoopBackOff containers | The container is currently waiting; the error lines are in the previous (crashed) run's log |
| `TailLines: 100` | Keeps memory use bounded; RAS errors and their preceding SEN lines always appear near the tail of a crashing container's log |
| 403/401 → permanent disable | Permission errors are operator configuration problems, not transient failures; retrying indefinitely would spam the API and logs |
| Generic watch errors → backoff + retry | Transient network failures, watch expiry, and server restarts are expected; the loop re-establishes the watch after 5 s |
| Filter pods client-side for `ibm.com/spyre_*` | `fieldSelector` cannot filter on resource requests; filtering is done after receiving the event |
| `ClusterRole` required for RBAC | Pod watch uses `fieldSelector: spec.nodeName=<NODE_NAME>` across all namespaces; a namespaced `Role` is insufficient |
| `pods/log` listed separately in RBAC | Kubernetes treats log streaming as a sub-resource; it must be explicitly granted alongside `pods` |

---

## Testing

Tests live entirely inside `package reporter` (white-box) and are driven by
[Ginkgo v2](https://onsi.github.io/ginkgo/) + Gomega. All test assertions about log
content use `scanLines(io.Reader)` directly — the fake kube client's `GetLogs` is
hardwired to return `"fake logs"` and cannot be intercepted via reactors.

### `ras_test.go` coverage

**`containsRASError`** — `DescribeTable`, 5 entries

| Entry | Scenario |
|---|---|
| Full RAS error line | Both markers present → `true` |
| Only `"name":"RAS::"` | Missing severity → `false` |
| Only `"severity":"ERROR"` | Missing name → `false` |
| Neither marker | Normal log line → `false` |
| Empty string | → `false` |

**`extractSENPCIAddress`** — `DescribeTable`, 4 entries

| Entry | Scenario |
|---|---|
| Full-domain address | `SEN:VFIO:TYPE1:0000:2e:00.0` → `"0000:2e:00.0"` |
| Short-form address | `SEN:VFIO:TYPE1:2e:00.0` → `"0000:2e:00.0"` (normalised) |
| No SEN pattern | RAS error line → `""` |
| Empty string | → `""` |

**`requestsSpyreResource`** — `DescribeTable`, 3 entries

| Entry | Scenario |
|---|---|
| Pod with `ibm.com/spyre_gpu` | → accepted |
| Pod with only `cpu` request | → rejected |
| Pod with no resource requests | → rejected |

**`isFailedOrCrashLooping`** — `DescribeTable`, 5 entries

| Entry | Scenario |
|---|---|
| `Failed` phase | → accepted |
| `CrashLoopBackOff` container | → accepted |
| `Running`, no crash | → rejected |
| `Pending`, no crash | → rejected |
| `Succeeded` | → rejected |

**`RASReporter.addRASError and Collect`** — `Describe`, 4 specs

| Spec | Scenario |
|---|---|
| Empty reporter | `Collect()` returns empty slice, no error |
| Single `addRASError` | Returns one `IN_ERROR` entry with `Source="ras"`, `Priority=PriorityRAS` |
| Same address twice | Idempotent — one entry |
| Two different addresses | Two entries |

**`RASReporter.scanLines`** — `DescribeTable`, 6 entries

| Entry | Scenario |
|---|---|
| SEN then RAS error | `"0000:2e:00.0"` recorded |
| RAS error, no prior SEN | Nothing recorded |
| SEN then non-RAS line | Nothing recorded |
| Two SEN+RAS pairs | Both addresses recorded |
| Empty log | Nothing recorded |
| `ras_timeout_log.txt` (real crash log) | `"0000:2e:00.0"` recorded |

**`RASReporter.watchPods lifecycle`** — `DescribeTable`, 8 entries (fake kube client)

| Entry | Scenario |
|---|---|
| `DELETED` event | Nothing recorded, not disabled |
| `MODIFIED` Running pod (no crash) | Nothing recorded |
| `ADDED` pod without spyre resource | Nothing recorded |
| `ADDED` Failed spyre pod | GetLogs called (fake stub → nothing recorded) |
| `ADDED` CrashLoopBackOff spyre pod | GetLogs called (fake stub → nothing recorded) |
| Watch returns 403 | `disabled=true` |
| Watch returns 401 | `disabled=true` |
| Watch returns generic error | Not disabled, `watchPods` returns |
| ctx cancelled | Exits cleanly, nothing recorded |

**`RASReporter.Start retry loop`** — `DescribeTable`, 3 entries

| Entry | Scenario |
|---|---|
| Watch 403 | Disabled after first call, no retries (`calls == 1`) |
| Watch 401 | Disabled after first call, no retries (`calls == 1`) |
| Watch channel immediately closed | Retries multiple times until ctx expires (`calls >= 2`) |

> **Implementation note:** the `calls` counter uses `atomic.Int64` (not a plain `int`)
> to avoid a data race between the reactor goroutine (write) and the test body (read)
> after `<-ctx.Done()`.

**`Merge with RASReporter`** — `DescribeTable`, 4 entries

| Entry | Scenario |
|---|---|
| Empty RASReporter + lspci ONLINE | `ONLINE` from lspci |
| RASReporter `IN_ERROR` + lspci `ONLINE`, same address | `IN_ERROR` from RAS (priority 10 wins) |
| RASReporter `IN_ERROR` + cardmgmt `IN_ERROR`, same address | `IN_ERROR` from RAS (higher priority wins) |
| RASReporter `IN_ERROR` for different address than lspci | Both entries present |

Run the full suite:

```bash
go test -race ./internal/reporter/... -v
```

---

## RBAC Requirements

The health-checker `ServiceAccount` requires the following additional `ClusterRole` rule
(managed in the `spyre-operator` repository):

```yaml
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
```

A `ClusterRole` (not a namespaced `Role`) is required because the watch uses
`fieldSelector: spec.nodeName=<NODE_NAME>` across all namespaces. `pods/log` must be
listed as a separate entry — Kubernetes treats log streaming as a distinct sub-resource.

## Risk

An unprivileged user who can create a pod in any namespace on the node can craft log
output containing the `SEN:VFIO:TYPE1:<pciAddress>` and RAS error markers. Because
the watcher covers all namespaces by default, a single malicious pod can cause a
healthy device to be recorded as `DEVICE_STATE_IN_ERROR`, making it unschedulable.
With multiple devices this is an effective denial-of-service against the Spyre device
pool on that node.

**Mitigation:**
Introduce a new CLI flag `--ras-watcher-limit-namespaces` that accepts a
comma-separated list of trusted namespaces. When set, the RAS watcher ignores pods
that originate from any namespace not in the list. The default (empty) preserves the
current behaviour of watching all namespaces.

| Flag | Default | Meaning |
|---|---|---|
| `--ras-watcher-limit-namespaces` | `""` (empty) | Comma-separated trusted namespaces. Empty = all namespaces (backwards-compatible). |

Operators deploying in multi-tenant clusters should set this flag to the namespace(s)
where legitimate Spyre workloads run, preventing tenant workloads in other namespaces
from influencing device state.
