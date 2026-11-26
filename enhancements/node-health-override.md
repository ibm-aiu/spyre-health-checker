# Node Health Override

**Milestone:** 1.5.0 (proposed)

## Overview

An administrator can force the reported health of every Spyre device on a node, or of single cards
(a PF and its VFs) on it, by adding one entry to a ConfigMap:

```bash
# force every Spyre device on worker-3 to IN_ERROR (not offered to workloads)
oc create configmap spyre-health-override -n <operator-namespace> --from-literal=worker-3=unhealthy

# force every Spyre device on worker-5 to ONLINE
oc patch configmap spyre-health-override -n <operator-namespace> \
  --type merge -p '{"data":{"worker-5":"healthy"}}'

# force two cards on worker-6 (the value is a file with one "<PF address> <value>" line per card)
oc set data configmap/spyre-health-override -n <operator-namespace> --from-file=worker-6=worker-6.txt

# back to normal for worker-3
oc patch configmap spyre-health-override -n <operator-namespace> \
  --type json -p '[{"op":"remove","path":"/data/worker-3"}]'
```

The ConfigMap is mounted into the health checker pods as a read-only volume. The health checker on
each node reads the file named after its own node, applies the override to the merged result of the
reporters, and publishes it through the existing gRPC stream and metrics. The device plugin and the
DRA driver need no code change: they see ordinary state changes. The states stay `ONLINE` and
`IN_ERROR`; a forced device is told apart by `source="override"` in the metrics.

## Motivation

Every input today is a detector (lspci, cardmgmt, RAS, driver check). An administrator who knows
something the detectors do not, such as a card about to be serviced or a false RAS error, has no
supported way to say so.

Alternatives considered:

- **File inside the pod (`oc exec`)**: lost on pod restart, needs `pods/exec` on a pod that mounts
  the TLS key, the pod name changes (DaemonSet), and the change is not recorded as an API object.
- **Node label or annotation**: rejected in review. Many users, operators and components may modify
  node labels, so the control point is too widely writable, and the health checker would need
  cluster-wide read access to `nodes`, which it has never had.
- **Custom resource**: more structure (status, per-card entries) than a node-level switch needs.
- **`SpyreClusterPolicy` field**: the most trusted place to configure it, since the policy is already admin-only.
  It needs an API/CRD change and operator code that renders this same ConfigMap. A possible second
  phase (see Later).
- **ConfigMap (chosen)**: writable only by whoever may update that one object, recorded in the API
  audit log, easy to keep in git, and read through a volume, so the health checker needs **no new
  RBAC**. The precedent is the optional `spyre-cardmgmt-config` ConfigMap of the card management
  DaemonSet.

## Contract

ConfigMap `spyre-health-override` in the operator namespace. One key per node, named exactly like
the node (a node name is a valid ConfigMap key). The operator never creates, changes or deletes it.

A PCI address is not a valid ConfigMap key (keys allow only letters, digits, `-`, `_` and `.`; checked
with `oc create configmap --dry-run=client`), so the per-device entries are lines in the value of the
node's key. The value is text, one line each:

| Line in the value of `<node name>` | Effect |
|---|---|
| `unhealthy` or `all unhealthy` | every device reported on the node becomes `IN_ERROR` |
| `healthy` or `all healthy` | every device reported on the node becomes `ONLINE` |
| `<PF address> unhealthy` | the PF and its VFs become `IN_ERROR` |
| `<PF address> healthy` | the PF and its VFs become `ONLINE` |
| `<VF address> ...` | refused: skipped with a warning that names the PF; the VF follows its PF's line or the whole-node line |
| blank, or text after `#` | ignored |
| anything else | skipped with a warning; the other lines still apply |

The bare words `healthy` and `unhealthy` are the form that the first version accepted, so existing
entries keep working. With no such key, or no ConfigMap, nothing changes.

Rules:

- **Precedence:** a line for a card wins over the line for the whole node. If a target appears twice,
  the last line counts.
- **Address form:** the full form that the reporters publish, `dddd:bb:ss.f` (four-digit domain, function
  0 to 7), in either case. A short form such as `1a:00.0` is refused rather than guessed, because
  lspci prints short addresses and a mismatch would silently do nothing.
- **A line names a PF and covers the PF and its VFs.** The VFs of a PF are the reported VFs in the same
  PCI slot (function 0 is the PF), by `reporter.PFAddressOf`, which is also how the cardmgmt reporter
  gives VFs the state of their PF, so the two rules cannot drift apart. It is a rule on the address, with
  no sysfs lookup (`physfn`), so it works the same in tests and on a node.
  Why a line covers the VFs: the health checker does not always copy a PF failure to its VFs (a RAS error
  is recorded for one address only, lspci and the driver check judge each function alone), while the
  device plugin does copy it and the DRA driver was not seen to. Naming the PF makes the three agree for
  the administrator's decision.
- **A line for a VF is refused**, with a warning that names the PF (or says the VF has no PF). The VF
  follows its PF's line or the whole-node line.
- **The function number decides, not the reported type.** Function 0 of a slot is the card and every
  other function is one of its VFs, even when a reporter typed it as a PF: the RAS reporter gives every
  address it records the type PF, and a RAS error that replaces a VF's lspci state brings that type
  with it. Without this rule a line for the PF would not reach such a VF, which is the case where a
  false RAS error needs clearing. Allowing VF lines later would break nobody; refusing
  them later would, so they start refused.
- **Isolated VFs are out of scope.** A VF that has no PF in its slot (function 0, as in the s390x
  layout) cannot be named. Only the whole-node lines reach it.
- **Never adds a device:** a line for a card that no reporter returned (neither the PF nor one of its
  VFs) is skipped with a warning. A line is used when the PF is missing but one of its VFs is reported.
- **Whitespace and case:** blanks, tabs, `\r\n` and a missing final newline are accepted; words are
  case-insensitive. The documentation uses lower case.
- **Unconditional:** both values have no expiry and no time filter, whatever the detectors say now or
  later.
- **YAML is avoided on purpose:** an unquoted `0000:10:00.0` is read as the number 600 by a YAML 1.1
  parser (seen with Ruby's Psych). A line format has no such trap, and `oc create configmap
  --from-file` stores it as a block value.

## Where it applies

In `Vitals.UpdateStates()`:

```
Merge(reporters)  ->  updateDriverStatus  ->  applyOverride  ->  published
```

- `Merge` cannot do this: an `IN_ERROR` is never turned back to `ONLINE`, and no priority beats RAS
  (10). The override is a separate step that rewrites the merged result.
- When a reporter fails, the override is still applied: `UpdateStates()` publishes a result anyway (the
  partial one at start-up, the last complete one later) and runs the driver check and the override on
  it. An administrator is most likely to write an override exactly when something is wrong, for
  example while the card management sidecar is down. The remembered result is kept without the
  override, so removing the override brings the reporters' view back.
- The override must not hide a card that has lost its driver. The driver check therefore runs first
  and returns the devices it failed; `healthy` skips those, and `unhealthy` forces `IN_ERROR` anyway.
  The result is the same as running the override before the driver check, as first designed. The
  order differs for one reason: the driver check skips states whose `Source` is `pseudo`, and the
  override sets `Source` to `override`. Running the driver check first keeps pseudo mode working
  without carrying the original source in a new field.
- PFs and VFs are rewritten alike. Address and type are kept, `State` is set, and `Source` becomes
  `override` (a device that `healthy` leaves alone because of its driver keeps its own source). Only
  devices the reporters returned are rewritten; the override cannot add a card. A card that
  disappears is still marked `REMOVED` by the server.
- The driver rule is per function: a node-wide `healthy` skips the functions without a driver quietly;
  a function that a card line asked to make healthy but that has no driver is skipped with a warning (the
  administrator asked for it, so the refusal is reported). A card whose PF has no driver but whose VFs do
  gets healthy VFs and an unhealthy PF, as the driver check alone would leave them.
- Addresses are matched in lower case on both sides, so `0000:1A:00.0` in the file finds the device
  that a reporter published in either case.
- `healthy` hides a RAS error, it does not clear it. The RAS error map is unchanged, so removing the
  entry brings the error back. Clearing a RAS error is the `healthy-since` work below.
- `healthy` can send work to a card that is not up (`OFFLINE`, `BOOTING`) or has a real fault. The
  docs say so and show the alert below.

### Why a separate step and not a reporter

- The rule in `Merge` is `s.Priority > existing.Priority && (existing.State == ONLINE || s.State != ONLINE)`:
  an `ONLINE` never replaces an `IN_ERROR`, whatever its priority. A reporter could force `unhealthy`
  (with a priority above RAS), but `healthy` would only work if that reporter ran first, and the order
  is just the order in `buildReporters` (RAS is always appended last). That is too fragile.
- Making it robust would need a special case in `Merge`, the function whose rule the docs call a
  safety property: no reporter can silently clear an error. A separate step leaves `Merge` unchanged
  and keeps the override visible in one place.
- A reporter would also have no device list. The cardmgmt reporter runs lspci itself to find its
  devices; a step after the merge rewrites the devices already found.

## Reading the file

- Mount: `/etc/spyre-health-checker/override` (read-only, optional ConfigMap volume, **no `subPath`**,
  otherwise kubelet never refreshes it). The file read is `<mount>/<NODE_NAME>`. The flag
  `--health-override-dir` (environment variable `HEALTH_OVERRIDE_DIR`) sets the directory; an empty
  value turns the feature off.
- A node name that is empty, `.`, `..` or contains `/` is refused, so the read cannot leave the
  directory.
- `UpdateStates()` reads the file on every check, right after `Merge`. The file is tiny, so there is
  no goroutine, no file watcher (`fsnotify` is not a dependency) and no extra channel in `main()`.
- The first check runs inside `server.NewServer(vitals)`, before the RAS watcher starts in `main()`.
  It reads the file too, so a restart never publishes the detector state without the override. The
  path is therefore set on `Vitals` (for example through `NewVitals`), not started later.
- The last entry read is kept in memory, to log changes and to survive a read error. A change is logged
  when its effect changes (the text of the log line is the effect, such as `0000:41:00.0 -> ONLINE`),
  so reformatting the file or adding a comment logs nothing.
- A change takes effect at the next check: up to one timer interval (30 s through the operator,
  `1h` for the bare binary) after kubelet has refreshed the mounted file, which takes up to about a
  minute. Expect a couple of minutes.

## Failure handling

| Situation | Behavior |
|---|---|
| No ConfigMap, empty volume, no file for this node, or no mount at all (operator change not deployed yet) | no override (the normal case). A missing file or directory means "absent", never an error |
| A file that was there is gone (entry or ConfigMap removed) | the override ends at the next check; the last value is **not** kept |
| `NODE_NAME` unset or unusable | feature off, one warning |
| File exists but cannot be read | keep the last entry, warn once per error |
| A line that cannot be used (typo, short address, unknown word) | skip that line, apply the others, warn once per line, count it in `mode="ignored"`; never exit (the value changes at runtime and a crash loop would cut off all health reporting for the node) |
| A line for an address no reporter returned, or `healthy` for a device without a driver | skip that line, warn once, count it in `mode="ignored"`. The warning is logged again if the problem goes away and comes back |
| A file with a very large number of bad lines | the first 10 are listed in the log, followed by one line that says more were skipped; all are counted |

No Kubernetes API access is involved, so there are no `403` or watch-reconnect cases.

## Making it hard to forget

- A `WARN` log line when the override is applied, changed or removed, naming the effect (for example
  `every device -> ONLINE, 0000:41:00.0 and its VFs -> IN_ERROR`).
- Gauge `spyre_health_override{node, mode}`: three series that always exist. `mode="healthy"` and
  `mode="unhealthy"` hold the number of devices forced to that state, and `mode="ignored"` holds the
  number of lines that could not be used plus the devices that a `healthy` line could not be applied to
  (no driver). All zero means no override. This replaces the first
  version's single series (value 1, `mode` one of `none|healthy|unhealthy|invalid`), which cannot say
  "two devices healthy, one unhealthy". The change is safe because the first version is not in a
  release yet.
- The per-device series carry `source="override"`.
- The admin guide suggests an alert on `mode="healthy"` above 0 for longer than a chosen time.

## Security

- **Who can set it:** whoever may update the ConfigMap, a single named object in the operator
  namespace, instead of anyone who may patch nodes. The change is in the API audit log. `unhealthy`
  withdraws a node from scheduling; `healthy` exposes bad cards, so access to this ConfigMap should
  be limited to administrators.
- **Narrow access:** a Role with `update` and `patch` limited by `resourceNames: [spyre-health-override]`
  works for editing. `create` cannot be limited by name, so an administrator creates the empty
  ConfigMap once.
- **Readers:** every health checker pod mounts the whole ConfigMap and so can see which nodes are
  overridden. This is not secret.
- **No new API permission** for the health checker.

## Operator change (spyre-operator)

`assets/state-init/spyre-health-checker/0400_daemonset.yaml` gets an optional ConfigMap volume and a
read-only mount into the `checker` container:

```yaml
volumes:
- name: health-override
  configMap:
    name: spyre-health-override
    optional: true
volumeMounts:
- name: health-override
  mountPath: /etc/spyre-health-checker/override
  readOnly: true
```

No ClusterRole, bundle or CSV change. Merge it before, or together with, the health checker change;
until then the mount is simply absent and the feature does nothing.

## Later (not in the first version)

A dashboard panel and alert rules for the `spyre_health_override` gauge are described in
[`future-enhancements.md`](future-enhancements.md).

- **Immediate re-check when the value changes**: a small re-read loop plus a second trigger next to
  the timer in `main()`, if waiting for the next check turns out to matter.
- **`healthy-since`**: with `healthy`, ignore only evidence older than a time. Without it, `healthy`
  stays unconditional, so nothing changes for existing users. This is also how a RAS error is acknowledged.
- **Timestamps on every recorded event.** For RAS, the evidence time must come from the pod (the
  container's `finishedAt`), not `time.Now()`: the watcher re-scans pods on every event and after a
  restart, so a `now()` stamp would make old errors look new.
- **Lines for VFs and for isolated VFs**: refused today. Accepting a VF line as an exception to its PF's
  line, or a line for a VF without a PF, would not break existing entries.
- **A sysfs-based PF/VF relation (`physfn`)** instead of the address rule, if a layout ever breaks the
  rule. The device plugin uses `physfn`.
- **Distinct states for "marked" devices** (`ONLINE_OVERRIDE` / `IN_ERROR_OVERRIDE`, or an additive
  field on the gRPC `Device`): see the second change in the same area. The consumers treat only
  `ONLINE` as healthy, so a new value must not reach them before they are updated.
- **RAS errors forgotten once the failing pod is gone.**
- **`SpyreClusterPolicy` field** that the operator renders into this ConfigMap. The operator would
  then own the ConfigMap, and manual edits would be overwritten.
- Whether a health-only change (`IN_ERROR` to `ONLINE`) reaches kubelet in the device plugin without
  a new allocation or hotplug event has not been checked on a cluster.

## Not verified yet

- That an optional ConfigMap created after the pod started appears in the volume without a restart,
  and how long a change takes.
- What kubelet does to the mounted files when the ConfigMap is deleted while the pod runs: files
  removed (which ends the override) or old content left in place (which would keep it).
  These need a quick test on a cluster.

## Tests

All in `internal/healthcheck/override_test.go`, plus the metric in `internal/utils/prometheus_test.go`
and the shared PF rule in `internal/reporter/cardmgmt_vf_test.go`.

- **Parsing** (`parseEntry`): bare value, `all <value>`, one line per card, address in either case,
  node line together with card lines, comments, blank lines, tabs, `\r\n`, no final newline, empty
  and comment-only files, the last line winning, ten kinds of bad line (typo, boolean, missing value,
  unknown word, short address, wrong function, trailing dot, wildcard, three words, `all` alone) that
  are skipped without losing the lines around them, a very long line, and the cap on listed problems.
- **Apply** (`applyOverride`): node-wide `healthy` and `unhealthy` over every state; a PF line that
  covers the PF and its VFs and nothing else; the VFs of another PF untouched; a PF line that still
  reaches its VFs when the PF itself is not reported; a card line beating the node line in both
  directions; a VF line refused (with the PF named), the VF following its PF's line or the node line; a
  VF without a PF that no line can name but the node line reaches; a device of unspecified type reached
  by its own address; case-insensitive matching; a missing driver on a PF, on a VF and node-wide (the
  driver is judged per function); a line for a card nobody reported (reported, never added); an empty
  entry; an empty device list.
- **File reader** with a temporary directory: missing directory, missing file, another node's file,
  the feature switched off, a read error keeps the previous entry once, file replaced by a symlink swap
  (as kubelet does), unsafe node names.
- **Logging** (`Override.Apply`): the start, change and end of an override are logged once; a file
  change with the same effect logs nothing; each problem is logged once and again if it returns.
- **`Vitals.UpdateStates` with a failing reporter** (`updatestates_test.go`): the partial result at
  start-up, the last complete result while running, the way back when the reporter returns, an
  override written, changed and removed while a reporter is down, the driver check on both results,
  and the remembered result not being changed by the override.
- **`Vitals.UpdateStates`** with the pseudo reporter, on the amd64 layout (a PF with two VFs) and the
  s390x layout (isolated VFs): no override, node-wide both ways, one card changed with its VFs and the
  rest as the reporter said, a refused VF line, an isolated VF reachable only by the node line, return
  to the reporters' states when the entry is removed, a bad line next to a good one, and a device without
  a driver (a real sysfs check) with both the node line and the card line.
- **Metric**: three series always present, the numbers replaced at every update, no panic before
  `InitMetrics`.

Not covered by a test: the first state published by `server.NewServer` including the override. The
order in `main()` (override attached before `NewServer`) is checked by reading the code only.

## Documentation to update with the code

`docs/reporters.md` and `docs/health-indicators.md` (the new step), `docs/deployment.md` (the volume),
`docs/configuration.md` (flag and environment variable), the README, and the admin guide in
`spyre-operator-docs` (how to create, change and remove an entry, the `healthy` warning, the alert
suggestion).
