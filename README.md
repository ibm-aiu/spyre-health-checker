# spyre-health-checker

Health Checker for AIU Spyre Cards

## Running the tests

### Prerequisites

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

### Run the full test suite

```bash
make test
```

This runs all Ginkgo test suites under `./...` with the race detector enabled,
generates `coverage.out` and `coverage-report.html`, and enforces a minimum
coverage threshold (currently 45%). A summary of per-function coverage is
printed at the end.

### Run a specific package

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

### What each suite covers

| Package | Suite | Key coverage |
|---|---|---|
| `internal/reporter` | `reporter_suite_test.go` | `Merge()` conflict resolution, all four override matrix cases, partial failure, LSPCIReporter parsing (14 devices), `stamp()`, `CardmgmtReporter`, `PseudoReporter` |
| `internal/reporter` | `ras_test.go` | `RASReporter`: log scanning, SEN/RAS marker extraction, pod qualification, watch lifecycle, permission-error disable, retry loop, `Merge` integration |
| `internal/reporter` | `cardmgmt_client_test.go` | `CardHealthClient` (mTLS): healthy/unhealthy mapping, error-code skipping, multi-slot, empty slot list, TLS cert-load failure |
| `internal/healthcheck` | `healthcheck_test.go` | `Vitals.UpdateStates()`, `GetVitalStates()`, driver-check skips pseudo sources |
| `pkg/server` | `server_test.go` | gRPC streaming (pseudo devices), `UpdateHealths`, concurrent vitals access, `/healthz`, `/readyz`, `/metrics`, removed-device detection, `RegisterForSpyreDevicesEventsWithDevices` |
| `pkg/server` | `server_suite_test.go` (mTLS table) | Accepts trusted cert, rejects no-cert client, rejects untrusted-CA cert, rejects cert with no Organisation |
| `internal/utils` | `prometheus_test.go` | `InitMetrics`, `UpdateDeviceMetrics`, gauge labels |

### Pseudo device mode in tests

All test suites that exercise the server or health-check pipeline set
`PSEUDO_DEVICE_MODE=1` in their `BeforeSuite` hook. This means the tests run
without real hardware or a live sidecar — the `PseudoReporter` returns a fixed
set of synthetic device states (see [Pseudo device mode](#pseudo-device-mode)).

## Requirements

The health checker requires the `lspci` command to gather information on Spyre cards.
When the `cardmgmt` reporter is enabled it also requires access to the
`aiu-cardmgmt-health-api` UNIX socket (see [cardmgmt reporter](#cardmgmt-reporter)).
When running inside Kubernetes the RAS pod watcher requires a `ClusterRole` with
`pods` and `pods/log` access (see [RBAC](#rbac)).

## Simple setup

This client-server system, using gRPC streaming, is a simplified implementation of that found in `https://github.com/ibm-aiu/spyre-device-plugin`.
The server runs on a UNIX socket (default `/usr/local/etc/device-plugins/health/checker.sock`) with mTLS.

The proto file can be edited and rebuilt via:

```sh
make protoc-gen
```

This project has a client and a server.

The server periodically runs health checks using a pluggable reporter framework
(see [Reporters](#reporters)) and streams device state updates over gRPC.

The periodic check timer can be set via the `--timer` flag in duration format,
such as `5s` or `1h40m` (default `1h`).

## Reporters

The health checker supports a pluggable reporter framework. Each reporter is an
independent source of `[]DeviceState`; results from all active reporters are
combined by `Merge()` using priority-based, safety-first conflict resolution (see
[Merge and priority rules](#merge-and-priority-rules) below).

Reporters are enabled via the `--enabled-reporters` flag (comma-separated):

| Reporter | Flag value | Priority | Description |
|---|---|---|---|
| LSPCIReporter | `lspci` | 1 (lowest) | Reads card state from `lspci -vvvnn` output (default) |
| CardmgmtReporter | `cardmgmt` | 5 | Queries the `aiu-cardmgmt-health-api` sidecar over a UNIX socket |
| RASReporter | always on | 10 (highest) | Watches Kubernetes pods for RAS hardware errors; always included regardless of `--enabled-reporters` |

### Merge and priority rules

`Merge()` builds a single authoritative device state map across all active
reporters. For each PCI address, a new entry from a higher-priority reporter
replaces the existing entry **with one exception: an error state is sticky**.

| Existing state | Incoming state | Higher priority? | Result |
|---|---|---|---|
| `ONLINE` | `IN_ERROR` | Yes | `IN_ERROR` — higher authority marks unhealthy |
| `ONLINE` | `ONLINE` | Yes | `ONLINE` — source and priority updated |
| `IN_ERROR` | `IN_ERROR` | Yes | `IN_ERROR` — higher authority updates attribution |
| `IN_ERROR` | `ONLINE` | Yes | **`IN_ERROR` kept** — no reporter can silently clear an error |

The last rule means that once any reporter marks a device `IN_ERROR`, that state
persists until the health-checker process restarts — no lower-priority reporter
reporting `ONLINE` can override it. This is intentional: a hardware error should
require explicit operator action to clear.

If a reporter's `Collect()` returns an error, it is skipped and the remaining
reporters still contribute their results. A single unavailable source never
blanks out the entire device list.

See [`enhancements/reporter-framework.md`](enhancements/reporter-framework.md)
for the full design specification.

### lspci reporter

Runs `lspci -vvvnn`, filters for IBM Spyre PF (`1014:06a7`) and VF (`1014:06a8`)
devices, and classifies each:

| Condition | State |
|---|---|
| Revision == `ff` | `DEVICE_STATE_IN_ERROR` |
| All other revisions | `DEVICE_STATE_ONLINE` |

After all reporters run, `healthcheck.go` additionally checks the kernel driver
symlink at `/sys/bus/pci/devices/<pciAddress>/driver` for each non-pseudo device.
If `os.Stat()` fails or times out (5 s), the device is forced to
`DEVICE_STATE_IN_ERROR` regardless of what any reporter returned. This check
runs outside the reporter pipeline and overrides all reporter results.

Priority: `PriorityLSPCI = 1`.

### cardmgmt reporter

The `cardmgmt` reporter calls `GetCardHealth` on the
[`aiu-cardmgmt-health-api`](../aiu-cardmgmt-health-api) sidecar (which runs in
the cardmgmt worker Pod on the same node) for each PCI slot discovered by
`lspci`. It maps the sidecar's `"healthy"` / `"unhealthy"` response to
`DEVICE_STATE_ONLINE` / `DEVICE_STATE_IN_ERROR`.

Priority: `PriorityCardmgmt = 5`.

**Flags / environment variables:**

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--cardhealth-socket` | `CARDHEALTH_GRPC_SOCKET` | `/var/run/cardmgmt-health-check-api/health-check-api.sock` | UNIX socket path of the sidecar |
| `--tls-cert` | `SPYRE_TLS_CERT` | `/etc/spyre-health-checker/certs/tls.crt` | Client certificate presented to the sidecar (same cert used for Channel A) |
| `--tls-key` | `SPYRE_TLS_KEY` | `/etc/spyre-health-checker/certs/tls.key` | Private key for the client certificate |
| `--tls-ca` | `SPYRE_TLS_CA` | `/etc/spyre-health-checker/certs/ca.crt` | CA used to verify the sidecar's server certificate |

The sidecar requires mutual TLS (`require_client_auth` set to `True`) and additionally
checks that the client certificate carries a non-empty Subject Organization
field (mirroring Channel A's `authorizeClientCert`). The same certificate
material used for the `spyre-health-checker` gRPC server (Channel A) is reused
here — certificates generated by `hack/gen-local-certs.sh` satisfy both
requirements (`extendedKeyUsage = serverAuth,clientAuth`, `O = SpyreDev`).

The socket path must point to the UNIX socket created by the
`aiu-cardmgmt-health-api` sidecar. See [Kubernetes deployment](#kubernetes-deployment)
below for how to wire this across Pod boundaries.

### RAS reporter

The RAS reporter is **always active** — it is unconditionally appended by
`buildReporters()` regardless of the `--enabled-reporters` value. It carries the
highest priority (`PriorityRAS = 10`) so that a RAS-detected hardware error always
overrides any `ONLINE` assertion from `lspci` (1) or `cardmgmt` (5).

**How it works:**

The reporter runs a background goroutine (started by `RASReporter.Start()` in
`main.go`) that watches the Kubernetes API for pods on the local node that:

1. Request at least one `ibm.com/spyre_*` resource, **and**
2. Are in `Failed` phase **or** have a container in `CrashLoopBackOff`.

When such a pod is detected, the reporter fetches the last 100 lines of each
failed/crash-looping container's log and scans them with a two-marker algorithm:

- **SEN line** — matches `SEN:VFIO:TYPE1:<pciAddress>` to identify which physical
  device the pod was using.
- **RAS error line** — matches `"name":"RAS::` **and** `"severity":"ERROR"`. When
  found after a SEN line, the PCI address from the SEN line is recorded as
  `DEVICE_STATE_IN_ERROR`.

Example log sequence that triggers an error recording:

```
INFO ... Reusing PfInterface forSEN:VFIO:TYPE1:0000:2e:00.0, usage = 2
ERRR ... {"name":"RAS::PCI::PCIeFailure","severity":"ERROR",...}
```

The in-memory error map is **never cleared** while the process is running. If
the health-checker Pod restarts, the map starts empty and is repopulated only
if the failing workload pod is still present with RAS error lines in its logs.

**Graceful degradation:**

- If the Kubernetes in-cluster config is unavailable (e.g. running outside a
  cluster), `Start()` is never called and `Collect()` returns empty with no
  error — the other reporters continue to function normally.
- If the Kubernetes API returns `403`/`401` on a `Watch` or `GetLogs` call,
  the reporter logs a warning and permanently disables its retry loop.
  `Collect()` continues to return any errors accumulated before the failure.
- Any other watch error triggers a 5-second back-off before the watch is
  re-established.

**Namespace filtering (security):**

By default the RAS watcher covers all namespaces. In a multi-tenant cluster this
means a workload pod in any namespace can craft log output with the `SEN:VFIO`
and RAS error markers to cause a healthy device to be recorded as
`DEVICE_STATE_IN_ERROR`, effectively denying it to other workloads on the node.

Restrict the watcher to trusted namespaces using `--ras-watcher-limit-namespaces`:

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--ras-watcher-limit-namespaces` | — | *(empty — all namespaces)* | Comma-separated list of namespaces the RAS watcher trusts |

Operators running in multi-tenant clusters should always set this to the
namespace(s) where legitimate Spyre workloads run.

See [`enhancements/ras-watcher-reporter.md`](enhancements/ras-watcher-reporter.md)
for the full design specification including RBAC rules and test coverage.

### Pseudo device mode

When `PSEUDO_DEVICE_MODE=1` is set, the hardware reporters (`lspci` and
`cardmgmt`) are replaced by `PseudoReporter`, which returns a fixed set of
synthetic device states without touching any hardware or sidecar. The RAS
reporter is still included so that RAS errors (if the pod watcher is running)
override pseudo-healthy states exactly as they would in production.

Use pseudo device mode for local development, CI pipelines, and testing on
hardware that does not have IBM Spyre cards.

**Simulated devices (non-s390x):**

| Set | PCI addresses | State |
|---|---|---|
| Good PF cards | `0000:1a:00.0` … `0000:40:00.0` (7 cards) | `ONLINE` |
| Bad PF card | `0000:41:00.0` | `IN_ERROR` |
| VFs | `.1` and `.2` suffixes of each PF address | Same state as parent PF |

**Simulated devices (s390x — SR-IOV VF only):**

| Set | PCI addresses | State |
|---|---|---|
| Good isolated VFs | `0001:00:00.0` … `0007:00:00.0` (7 VFs) | `ONLINE` |
| Bad isolated VF | `0008:00:00.0` | `IN_ERROR` |

```bash
export PSEUDO_DEVICE_MODE=1
./spyre-health-checker --enabled-reporters=lspci,cardmgmt
# lspci and cardmgmt are silently replaced by PseudoReporter
```

## RBAC

The RAS reporter requires access to the Kubernetes API. The
`spyre-health-checker` ServiceAccount must be bound to a `ClusterRole`
(not a namespaced `Role`) containing:

```yaml
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
```

A `ClusterRole` is required because the pod watch uses a
`fieldSelector: spec.nodeName=<NODE_NAME>` that spans all namespaces. `pods/log`
must be listed separately — Kubernetes treats log streaming as a distinct
sub-resource.

Without this `ClusterRole`, the watch call returns `403 Forbidden` at startup,
the reporter logs a warning and permanently disables itself; all other reporters
continue to function normally.

The RBAC resources are managed in the `spyre-operator` repository.

## TLS reference

### Overview: two separate TLS channels

This system uses TLS in two distinct places. Understanding the difference is
essential before configuring certificates or troubleshooting connection errors.

```
┌────────────────────────────────────────────────────────────────────────┐
│  Channel A — spyre-health-checker gRPC server (always mTLS)            │
│                                                                        │
│  External client ──mTLS──► spyre-health-checker                        │
│  (e.g. spyre-device-plugin)   (pkg/server/server.go)                   │
│                                                                        │
│  Flags: --tls-cert  --tls-key  --tls-ca                                │
│  Env:   SPYRE_TLS_CERT  SPYRE_TLS_KEY  SPYRE_TLS_CA                    │
│  Always required. No insecure mode.                                    │
└────────────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────────────┐
│  Channel B — cardmgmt reporter → aiu-cardmgmt-health-api sidecar       │
│             (mTLS UNIX socket)                                         │
│                                                                        │
│  spyre-health-checker ──mTLS──► cardmgmt sidecar                       │
│  (internal/reporter/cardmgmt_client.go)  (src/health_check_api.py)     │
│                                                                        │
│  Flags: --cardhealth-socket  --tls-cert  --tls-key  --tls-ca           │
│  Env:   CARDHEALTH_GRPC_SOCKET  SPYRE_TLS_CERT  SPYRE_TLS_KEY          │
│         SPYRE_TLS_CA                                                   │
│  mTLS required. Client cert must have non-empty Subject Organization.  │
│  Same cert/key/CA files as Channel A.                                  │
└────────────────────────────────────────────────────────────────────────┘
```

Both channels are over UNIX domain sockets, which means traffic never
crosses the network. The same certificate material satisfies both channels
because `tls.crt` is issued with `extendedKeyUsage = serverAuth,clientAuth`.

---

### Certificate requirements (Channel A)

| Requirement | Value | Where enforced |
|---|---|---|
| Minimum TLS version | TLS 1.2 | Server sets `MinVersion: tls.VersionTLS12` |
| Client authentication | Required (mTLS) | Always required — no insecure mode |
| Certificate format | PEM | All cert/key files |
| Client cert `Organisation` field | **Must be non-empty** | Post-handshake interceptor in `pkg/server/server.go` (`authorizeClientCert`) |

The **`Organisation` field check** is the most common source of unexpected
`Unauthenticated` errors. The TLS handshake itself may succeed (the cert is
CA-trusted), but the gRPC stream interceptor (`authorizeStream` /
`authorizeUnary`) then inspects the peer certificate and rejects any client
whose cert has an empty `Subject.Organization`.

For `gen-local-certs.sh`-generated certs the `Organisation` is set to
`SpyreDev`, so they pass. Custom or corporate-CA-issued certs must also
include a non-empty `O=` field.

---

### Certificate files produced by `gen-local-certs.sh`

Running `make gen-local-certs` (or `bash hack/gen-local-certs.sh`) produces
the following files in `./certs/`:

| File | Purpose | Used as |
|---|---|---|
| `ca.crt` | Self-signed CA certificate | `--tls-ca` |
| `ca.key` | CA private key | Not used at runtime — keep offline |
| `tls.crt` | Server + client certificate signed by `ca.crt` | `--tls-cert` |
| `tls.key` | Private key for `tls.crt` | `--tls-key` |
| `fake-client.crt` | Self-signed cert from an **untrusted** CA | Testing: verifies the server rejects unknown CAs |
| `fake-client.key` | Private key for `fake-client.crt` | Testing only |

> **Dev only:** The generated certs have a 10-year validity and 4096-bit RSA
> keys. They are intended for local development and testing only — do not use
> them in production.

---

### Mapping flags to certificate files

**Channel A — spyre-health-checker server** (same flags also drive Channel B):

```bash
./spyre-health-checker \
  --tls-cert=certs/tls.crt  \   # server cert (Channel A) AND client cert (Channel B)
  --tls-key=certs/tls.key   \   # private key for both roles
  --tls-ca=certs/ca.crt         # CA used to verify clients (A) and the sidecar server (B)
```

---

### Production certificate guidance

For production deployments replace the self-signed certs with certificates
issued by your organisation's CA or a certificate manager (e.g. cert-manager,
Vault PKI). The certificates must satisfy:

- `O=` (Organisation) field is **non-empty** (required by the post-handshake
  check in `authorizeClientCert`).
- Extended key usage includes both `serverAuth` and `clientAuth`.
- The CA certificate used to verify clients matches the CA that signed the
  client certificates.

Store certificates in Kubernetes Secrets and mount them as volumes. Rotate
certificates before expiry by updating the Secret — the process must be
restarted to pick up new cert files (there is no hot-reload).

---

### Troubleshooting TLS

#### Step 1 — identify the failure

| Symptom | Cause |
|---|---|
| `spyre-health-checker` fails to start with `failed to load TLS credentials` | Channel A — server cert/key files missing or unreadable |
| Clients connecting to `spyre-health-checker` get `Unavailable` immediately | Channel A — TLS handshake failure (wrong CA, expired cert, no client cert) |
| Clients get `Unauthenticated` after connecting | Channel A — post-handshake check: client cert has empty `Organisation` field |

#### Step 2 — verify certificates with openssl

```bash
# Inspect a certificate (check CN, O=, validity, SANs, extended key usage)
openssl x509 -in certs/tls.crt -noout -text | grep -A5 "Subject:\|Validity\|Extended Key\|Subject Alt"

# Verify a cert was signed by a given CA
openssl verify -CAfile certs/ca.crt certs/tls.crt

# Test the Channel A handshake against the running server socket
openssl s_client -unix /usr/local/etc/device-plugins/health/checker.sock \
  -cert certs/tls.crt -key certs/tls.key -CAfile certs/ca.crt
```

#### Step 3 — test rejection paths with fake-client.crt

```bash
# Should fail with "certificate signed by unknown authority" or similar
openssl s_client -unix /usr/local/etc/device-plugins/health/checker.sock \
  -cert certs/fake-client.crt -key certs/fake-client.key -CAfile certs/ca.crt
```

#### Common errors reference

| Channel | Error message | Cause | Fix |
|---|---|---|---|
| A | `failed to load TLS credentials` | cert or key file missing/unreadable at startup | Check file paths and permissions; confirm files exist |
| A | `certificate signed by unknown authority` | Client CA doesn't trust the server cert | Ensure `--tls-ca` points to the CA that signed the client cert |
| A | `tls: certificate required` | Server requires client cert but none was presented | Supply `--tls-cert` and `--tls-key` on the client |
| A | `rpc error: code = Unauthenticated` | Client cert passed TLS handshake but has empty `Organisation` field | Add `O=<value>` to the client certificate's subject |
| A | `rpc error: code = Unavailable` | TLS handshake failed before any RPC was attempted | Check cert validity, CA trust chain, and minimum TLS version (1.2 required) |
| B | `cardhealth: load client cert/key: …` | `--tls-cert` / `--tls-key` files missing or unreadable | Confirm the files exist and are readable by the health-checker process |
| B | `cardhealth: read CA cert: …` | `--tls-ca` file missing or unreadable | Confirm the CA file exists and is readable |
| B | `cardhealth: no valid CA certificate found` | CA file exists but contains no parseable PEM certificate | Verify the file with `openssl x509 -in certs/ca.crt -noout -text` |
| B | `rpc error: code = Unauthenticated` (from sidecar) | Client cert has empty `Organisation` field | Add `O=<value>` to the client certificate's subject |
| B | `rpc error: code = Unavailable` (from sidecar) | mTLS handshake failed — wrong CA, expired cert, or no client cert | Ensure the same CA signs both the sidecar server cert and this client cert |

---

## Environment variables and flags reference

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--socket` | — | `/usr/local/etc/device-plugins/health/checker.sock` | Server UNIX socket |
| `--timer` | — | `1h` | Periodic health-check interval |
| `--enabled-reporters` | — | `lspci` | Comma-separated active reporters (`lspci`, `cardmgmt`) |
| `--health-port` | — | `8080` | HTTP port for `/healthz` and `/readyz` probes |
| `--metrics-port` | — | `8081` | HTTP port for Prometheus `/metrics` |
| `--tls-cert` | `SPYRE_TLS_CERT` | `/etc/spyre-health-checker/certs/tls.crt` | Server TLS certificate |
| `--tls-key` | `SPYRE_TLS_KEY` | `/etc/spyre-health-checker/certs/tls.key` | Server TLS private key |
| `--tls-ca` | `SPYRE_TLS_CA` | `/etc/spyre-health-checker/certs/ca.crt` | CA certificate for client verification |
| `--ras-watcher-limit-namespaces` | — | *(empty — all)* | Comma-separated trusted namespaces for the RAS watcher |
| `--cardhealth-socket` | `CARDHEALTH_GRPC_SOCKET` | `/var/run/cardmgmt-health-check-api/health-check-api.sock` | cardmgmt sidecar UNIX socket |
| `PSEUDO_DEVICE_MODE` | — | *(unset)* | Set to `1` to replace hardware reporters with pseudo devices |
| `NODE_NAME` | — | *(must be set in-cluster)* | Node name used by the RAS pod watcher to filter pods to the local node |

## Kubernetes deployment

Both `spyre-health-checker` and `aiu-cardmgmt-health-api` run one instance per
node (DaemonSet). Because the `aiu-cardmgmt-health-api` sidecar runs inside
the **cardmgmt worker Pod** (not this one), the UNIX socket it creates on the
host filesystem must be made accessible to the `spyre-health-checker` Pod via a
`hostPath` volume.

### Node-level socket path

The `aiu-cardmgmt-health-api` sidecar writes its socket to
`/var/run/cardmgmt-health-check-api/health-check-api.sock` inside its
container. Mount the parent directory from the host into both Pods so the
path is shared:

```
Host node filesystem
  /usr/local/etc/spyre-cardmgmt/         ← hostPath (node-level backing dir)

cardmgmt worker Pod                      spyre-health-checker Pod
  hostPath → /var/run/cardmgmt-health-check-api   hostPath → /var/run/cardmgmt-health-check-api
               health-check-api.sock  ←  created by aiu-cardmgmt-health-api at startup
```

### cardmgmt worker Pod (abbreviated)

The `aiu-cardmgmt-health-api` container must mount the host directory so its
socket file lands on the node filesystem:

```yaml
volumes:
  - name: health-check-api-socket
    hostPath:
      path: /usr/local/etc/spyre-cardmgmt
      type: DirectoryOrCreate
  - name: spyre-cert
    secret:
      secretName: spyre-cert
      optional: true

containers:
  - name: cardmgmt-worker
    # ... aiu-cardmgmt image

  - name: health-api
    # ... aiu-cardmgmt-health-api image
    env:
      - name: API_GRPC_SOCKET_PATH
        value: /var/run/cardmgmt-health-check-api/health-check-api.sock
    volumeMounts:
      - name: health-check-api-socket
        mountPath: /var/run/cardmgmt-health-check-api
      - name: spyre-cert
        mountPath: /etc/cardmgmt-health-check-api/certs
        readOnly: true
```

### spyre-health-checker Pod (abbreviated)

Mount the same host directory, the shared TLS certificates, and point the
reporter at the socket:

```yaml
volumes:
  - name: health-check-api-socket
    hostPath:
      path: /usr/local/etc/spyre-cardmgmt
      type: Directory        # created by the cardmgmt worker Pod; must exist before this Pod starts
  - name: spyre-certs
    secret:
      secretName: spyre-health-checker-certs   # contains tls.crt, tls.key, ca.crt

containers:
  - name: spyre-health-checker
    # ... spyre-health-checker image
    env:
      - name: NODE_NAME
        valueFrom:
          fieldRef:
            fieldPath: spec.nodeName   # required by the RAS pod watcher
    args:
      - --enabled-reporters=lspci,cardmgmt
      - --cardhealth-socket=/var/run/cardmgmt-health-check-api/health-check-api.sock
      # TLS flags apply to both Channel A (this server) and Channel B (sidecar client).
      # The default paths below match the volumeMount; override with SPYRE_TLS_* env vars
      # if you prefer not to use args.
      - --tls-cert=/etc/spyre-health-checker/certs/tls.crt
      - --tls-key=/etc/spyre-health-checker/certs/tls.key
      - --tls-ca=/etc/spyre-health-checker/certs/ca.crt
    volumeMounts:
      - name: health-check-api-socket
        mountPath: /var/run/cardmgmt-health-check-api
        readOnly: true
      - name: spyre-certs
        mountPath: /etc/spyre-health-checker/certs
        readOnly: true
```

> **Note:** The `aiu-cardmgmt-health-api` sidecar `chmod 0666`s its socket at
> startup so the health-checker container can reach it regardless of UID.
> The mTLS handshake then provides the actual authentication layer — the socket
> permissions are only needed for the filesystem-level connect(2) call.
> Ensure the cardmgmt Pod starts (and the socket exists on the host) before
> the health-checker Pod attempts its first collect cycle.

## Indicators of Spyre Card Health

The overall health decision for each device is built in two stages:

**Stage 1 — reporter pipeline** (see [Reporters](#reporters) above):

- `lspci`: device is `ONLINE` unless revision is `ff` (→ `IN_ERROR`).
- `cardmgmt`: device is `ONLINE` if the sidecar reports `"healthy"`, otherwise
  `IN_ERROR`.
- `ras`: device is `IN_ERROR` if a RAS hardware error was detected in a workload
  pod's logs on this node. This overrides any `ONLINE` from `lspci` or `cardmgmt`
  and cannot be cleared until the health-checker restarts.

**Stage 2 — driver check** (`internal/healthcheck/healthcheck.go`):

After merging reporter results, the health-checker verifies the kernel driver
symlink for every non-pseudo device:

```
/sys/bus/pci/devices/<pciAddress>/driver
```

If `os.Stat()` fails or does not complete within 5 seconds, the device is forced
to `DEVICE_STATE_IN_ERROR` regardless of what the reporters returned. This catches
cards that are visible to `lspci` and the sidecar but have lost their kernel
driver binding.

**lspci filter rules** (Stage 1 detail):

- Devices where vendor:device ID is neither `1014:06a7` (PF) nor `1014:06a8` (VF)
  are excluded.
- Devices where revision (`REV`) is not `01` are excluded (rev `01` is a
  known-good boot state for Spyre).
- Revision `ff` → `DEVICE_STATE_IN_ERROR`.

**Possible future indicators** (not currently implemented):

- Power state not `D0` → offline.
- `SERR+`, `TAbort+`, `MAbort+`, or `FatalErr+` set → error.
- Kernel driver path from `lspci` output validated for correctness.

## Further reading

Detailed design documents are in the [`enhancements/`](enhancements/) directory:

- [`enhancements/reporter-framework.md`](enhancements/reporter-framework.md) —
  reporter interface, `Merge()` algorithm, override matrix, design decisions, and
  test coverage for `LSPCIReporter` and `CardmgmtReporter`.
- [`enhancements/ras-watcher-reporter.md`](enhancements/ras-watcher-reporter.md) —
  full RAS reporter specification: pod qualification, log scanning algorithm, RBAC,
  security risk and mitigation, retry behaviour, and complete test coverage.

See also [TLS reference](#tls-reference) in this README for the full certificate
requirements, flag-to-file mapping, production guidance, and troubleshooting table.

## License
This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
