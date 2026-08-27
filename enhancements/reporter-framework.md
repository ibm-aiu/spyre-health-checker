# Reporter Framework

**Milestone:** 1.4.0

## Overview

Introduce a `reporter` package (`internal/reporter`) that decouples device-state
collection from the health-check loop. Each *reporter* is a pluggable source of
`[]types.DeviceState`; a central `Merge` function combines results from all active
reporters into a single authoritative view using priority-based, safety-first conflict
resolution.

---

## Motivation

Previously, device-state collection logic was co-located with the health-check loop
and the gRPC server. This made it difficult to:

- Add new collection sources (e.g. card management service) without touching server
  code.
- Test conflict-resolution rules in isolation.
- Reason about which source "owns" the current state for a given device.

The reporter framework addresses all three points by providing a uniform interface,
stamped metadata, and a well-specified merge algorithm.

---

## Key Concepts

### `types.Reporter` interface

Every device-state source implements three methods:

```go
type Reporter interface {
    Name()     string
    Priority() int
    Collect()  ([]DeviceState, error)
}
```

| Method | Purpose |
|---|---|
| `Name()` | Human-readable identifier; written to `DeviceState.Source` by `stamp`. |
| `Priority()` | Numeric priority; higher value wins on conflict (see Merge rules). |
| `Collect()` | Returns the current device states, or an error if the source is unavailable. |

### `types.DeviceState`

```go
type DeviceState struct {
    PciAddress string
    Type       pb.DEVICE_TYPE
    State      pb.DEVICE_STATE
    Source     string   // set by stamp(), e.g. "lspci", "cardmgmt"
    Priority   int      // mirrors the producing reporter's priority
}
```

`Source` and `Priority` are always overwritten by `stamp()` inside each reporter's
`Collect()` implementation so callers never need to set them manually.

### Priority levels

Defined in [`pkg/types/types.go`](../pkg/types/types.go):

| Constant | Value | Reporter |
|---|---|---|
| `PriorityLSPCI` | 1 | `LSPCIReporter` — hardware scan (lowest) |
| `PriorityCardmgmt` | 5 | `CardmgmtReporter` — card management service |

Higher numeric value = higher authority.

---

## Reporters

### `LSPCIReporter` (`internal/reporter/lspci.go`)

Runs `lspci -vvvnn`, parses the output stanza-by-stanza, and returns a
`DeviceState` for every IBM Spyre PF (`1014:06a7`) or VF (`1014:06a8`) device found.

State classification:

| Condition | `DEVICE_STATE` |
|---|---|
| Revision == `ff` | `DEVICE_STATE_IN_ERROR` |
| All other revisions | `DEVICE_STATE_ONLINE` |

Priority: `PriorityLSPCI = 1`.

### `CardmgmtReporter` (`internal/reporter/cardmgmt.go`)

Delegates collection to an injected `CollectFn func() ([]types.DeviceState, error)`.
The indirection makes the reporter testable without a live card management service.
When `CollectFn` is nil, `Collect()` returns `nil, nil` (no-op).

Priority: `PriorityCardmgmt = 5`.

---

## `Merge` — Conflict Resolution

```go
func Merge(reporters []types.Reporter) ([]types.DeviceState, error)
```

`Merge` iterates reporters in order, collects their states, and builds a
`map[pciAddress]DeviceState` applying the following rules per device:

### Override matrix

For a given PCI address, when a new entry (from a higher-priority reporter) meets an
existing entry already in the map:

| Existing state | Incoming state | Override? | Rationale |
|---|---|---|---|
| `ONLINE` | `IN_ERROR` | **Yes** | Higher authority marks device unhealthy. |
| `ONLINE` | `ONLINE` | **Yes** | Higher authority confirms healthy; source/priority updated. |
| `IN_ERROR` | `IN_ERROR` | **Yes** | Higher authority updates error attribution. |
| `IN_ERROR` | `ONLINE` | **No** | Unhealthy is sticky — no reporter can silently clear an error. |

The rule expressed in code ([`reporter.go:71`](../internal/reporter/reporter.go:71)):

```go
if existing, ok := best[s.PciAddress]; !ok ||
    (s.Priority > existing.Priority &&
        (existing.State == spyre.DEVICE_STATE_ONLINE || s.State != spyre.DEVICE_STATE_ONLINE)) {
    best[s.PciAddress] = s
}
```

Decoded: override when there is no existing entry **or** (higher priority **and** it is
not the blocked case of `existing=ERROR, incoming=ONLINE`).

### Equal-priority tie-break

When two reporters share the same priority value, **the first encountered is kept**.
The incoming entry is never substituted regardless of its state.

### Error accumulation

If a reporter's `Collect()` returns an error, that reporter is skipped and its name
is recorded. `Merge` continues processing the remaining reporters and returns:

- Partial results from successful reporters.
- A combined error: `"merge encountered N reporter error(s): [...]"`.

This means a single unavailable source never prevents the health view from being
published.

---

## File Layout

```
internal/reporter/
├── reporter.go          # stamp(), Merge()
├── lspci.go             # LSPCIReporter + lspci output parser
├── cardmgmt.go          # CardmgmtReporter, SimplifiedDevice, CardManagement
├── reporter_test.go     # Merge table-driven tests
├── lspci_test.go        # parseLSPCI + stamp + Merge smoke tests
├── reporter_suite_test.go
└── testdata/
    └── lspci_input.txt  # sample lspci -vvvnn output used by lspci_test.go

pkg/types/types.go       # Reporter interface, DeviceState, priority constants
```

---

## Design Decisions

| Decision | Rationale |
|---|---|
| `stamp()` called inside `Collect()`, not by `Merge` | Each reporter is self-contained; callers of `Collect()` alone always get properly annotated states. |
| `CollectFn` injection on `CardmgmtReporter` | Avoids hard-wiring the card management RPC; unit tests supply a simple lambda. |
| `IN_ERROR` is sticky (cannot be cleared by a higher-priority ONLINE) | Safety-first: a hardware-detected error cannot be silently overruled by a higher-level service reporting healthy. Clearing requires an explicit operator action (e.g. `DELETE /override/<pci>`). |
| Partial results on reporter failure | A temporarily unavailable source (e.g. card management down) should not blank out the entire device list. |
| Priority as an integer constant, not an enum | Keeps the interface open; future reporters can slot in at any numeric level without changing existing constants. |

---

## Testing

Tests live entirely inside `package reporter` (white-box) and are driven by
[Ginkgo v2](https://onsi.github.io/ginkgo/) + Gomega.

### `reporter_test.go` — `DescribeTable` coverage

**`Merge`** — all entries use the shared `mergeTC` struct so data and expectations
are co-located.

| Entry | Scenario |
|---|---|
| no reporters | Empty result, no error |
| single reporter, no states | Empty result |
| single reporter, two devices | Both returned |
| `error→online` | Existing=ERROR, incoming=ONLINE — **not overridden** (sticky) |
| `online→error` | Existing=ONLINE, incoming=ERROR — **overridden** |
| `online→online` | Both ONLINE, higher priority — **overridden** |
| `error→error` | Both ERROR, higher priority — **overridden** |
| equal priority | First encountered kept regardless of state |
| non-conflicting addresses | All devices present |
| partial failure | Error accumulated; healthy reporter's results still returned |
| all reporters fail | Error returned, result empty |
| all four transitions, four devices | `wantByAddr` map asserts per-address source and state |

### `lspci_test.go` — parser and smoke tests

| Test | What is verified |
|---|---|
| `parseLSPCI` | 14 supported devices parsed; unsupported VDIDs excluded; revision `ff` → `IN_ERROR`; VF VDID → `DEVICE_TYPE_VF` |
| `stamp` | All entries get `Source="lspci"` and `Priority=PriorityLSPCI` |
| `Merge` smoke — `online→error` | `CardmgmtReporter` (priority 5) overrides `LSPCIReporter` (priority 1) when existing is ONLINE |
| `Merge` smoke — non-conflicting | Two reporters on different addresses → both present |

Run the full suite:

```bash
go test ./internal/reporter/... -v
```
