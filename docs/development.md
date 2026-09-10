# Development guide

## Prerequisites

```bash
# Install Ginkgo CLI and setup-envtest into ./bin/
make ginkgo envtest

# Vendor dependencies (required before building or testing)
make vendor

# Generate TLS certs used by the server test suite
make gen-local-certs
```

> `make gen-local-certs` only needs to be run once per clone. The server test
> suite generates its own in-memory certs via `createTestCertificates()` in
> `pkg/server/server_suite_test.go`, so it does not depend on the files on disk.
> The certs on disk are for running the binary manually.

## Run the full test suite

```bash
make test
```

This runs all Ginkgo test suites under `./...` with the race detector enabled,
generates `coverage.out` and `coverage-report.html`, and enforces a minimum
coverage threshold (currently 45%). A summary of per-function coverage is
printed at the end.

## Run a specific package

```bash
# reporter package only (LSPCIReporter, CardmgmtReporter, RASReporter, cardmgmt client)
go test -race ./internal/reporter/... -v

# healthcheck package
go test -race ./internal/healthcheck/... -v

# server package (gRPC streaming, mTLS handshake, HTTP probes)
go test -race ./pkg/server/... -v

# prometheus metrics utilities
go test -race ./internal/utils/... -v
```

## What each suite covers

| Package | Suite | Key coverage |
|---|---|---|
| `internal/reporter` | `reporter_suite_test.go` | `Merge()` conflict resolution, all four override matrix cases, partial failure, LSPCIReporter parsing (14 devices), `stamp()`, `CardmgmtReporter`, `PseudoReporter` |
| `internal/reporter` | `ras_test.go` | `RASReporter`: log scanning, SEN/RAS marker extraction, pod qualification, watch lifecycle, permission-error disable, retry loop, `Merge` integration |
| `internal/reporter` | `cardmgmt_client_test.go` | `CardHealthClient` (mTLS): healthy/unhealthy mapping, error-code skipping, multi-slot, empty slot list, TLS cert-load failure |
| `internal/healthcheck` | `healthcheck_test.go` | `Vitals.UpdateStates()`, `GetVitalStates()`, driver-check skips pseudo sources |
| `pkg/server` | `server_test.go` | gRPC streaming (pseudo devices), `UpdateHealths`, concurrent vitals access, `/healthz`, `/readyz`, `/metrics`, removed-device detection, `RegisterForSpyreDevicesEventsWithDevices` |
| `pkg/server` | `server_suite_test.go` (mTLS table) | Accepts trusted cert, rejects no-cert client, rejects untrusted-CA cert, rejects cert with no Organisation |
| `internal/utils` | `prometheus_test.go` | `InitMetrics`, `UpdateDeviceMetrics`, gauge labels |

## Pseudo device mode in tests

All test suites that exercise the server or health-check pipeline set
`PSEUDO_DEVICE_MODE=1` in their `BeforeSuite` hook. This means the tests run
without real hardware or a live sidecar -- the `PseudoReporter` returns a fixed
set of synthetic device states (see [Reporters -- Pseudo device mode](reporters.md#pseudo-device-mode)).

## Building the binary

```bash
# Standard build
go build -o spyre-health-checker ./cmd/health-checker/

# Smaller binary for copying to a remote node
go build -ldflags="-s -w" -o spyre-health-checker ./cmd/health-checker/
```

> Note: `go build ./...` does **not** produce a binary -- always specify the
> output path and the `./cmd/health-checker/` target explicitly.

## Regenerating protobuf code

```bash
make protoc-gen
```

This rebuilds the Go gRPC stubs from the `.proto` files using `buf`.

## Design documents

Detailed design documents are in the [`enhancements/`](../enhancements/) directory:

- [`enhancements/reporter-framework.md`](../enhancements/reporter-framework.md) --
  reporter interface, `Merge()` algorithm, override matrix, design decisions, and
  test coverage for `LSPCIReporter` and `CardmgmtReporter`.
- [`enhancements/ras-watcher-reporter.md`](../enhancements/ras-watcher-reporter.md) --
  full RAS reporter specification: pod qualification, log scanning algorithm,
  RBAC, security risk and mitigation, retry behaviour, and complete test coverage.
