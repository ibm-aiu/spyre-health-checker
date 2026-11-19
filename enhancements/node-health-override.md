# Node Health Override

**Milestone:** 1.5.0 (proposed)

## Overview

An administrator can force the reported health of every Spyre device on a node by adding one entry
to a ConfigMap:

```bash
# force every Spyre device on worker-3 to IN_ERROR (not offered to workloads)
oc create configmap spyre-health-override -n <operator-namespace> --from-literal=worker-3=unhealthy

# force every Spyre device on worker-5 to ONLINE
oc patch configmap spyre-health-override -n <operator-namespace> \
  --type merge -p '{"data":{"worker-5":"healthy"}}'

# back to normal for worker-3
oc patch configmap spyre-health-override -n <operator-namespace> \
  --type json -p '[{"op":"remove","path":"/data/worker-3"}]'
```

The ConfigMap is mounted into the health checker pods as a read-only volume. The health checker on
each node reads the file named after its own node, applies the override to the merged result of the
reporters, and publishes it through the existing gRPC stream and metrics. The device plugin and the
DRA driver need no code change: they see ordinary state changes.

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

| Value of the key `<node name>` | Effect on that node |
|---|---|
| no such key, or no ConfigMap | none |
| `unhealthy` | every device reported on the node becomes `IN_ERROR` |
| `healthy` | every device reported on the node becomes `ONLINE` |
| anything else | ignored, as if absent, and flagged as invalid (see Failure handling) |

Surrounding whitespace and a trailing newline are ignored, and the value is case-insensitive; the
documentation uses lower case. The scope is the whole node. Both values are unconditional: no expiry and no
time filter, whatever the detectors say now or later.

## Where it applies

In `Vitals.UpdateStates()`:

```
Merge(reporters)  ->  updateDriverStatus  ->  applyOverride  ->  published
```

- `Merge` cannot do this: an `IN_ERROR` is never turned back to `ONLINE`, and no priority beats RAS
  (10). The override is a separate step that rewrites the merged result.
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
- The last value read is kept in memory, to log changes and to survive a read error.
- A change takes effect at the next check: up to one timer interval (30 s through the operator,
  `1h` for the bare binary) after kubelet has refreshed the mounted file, which takes up to about a
  minute. Expect a couple of minutes.

## Failure handling

| Situation | Behavior |
|---|---|
| No ConfigMap, empty volume, no file for this node, or no mount at all (operator change not deployed yet) | no override (the normal case). A missing file or directory means "absent", never an error |
| A file that was there is gone (entry or ConfigMap removed) | the override ends at the next check; the last value is **not** kept |
| `NODE_NAME` unset or unusable | feature off, one warning |
| File exists but cannot be read | keep the last value, warn once per error |
| Invalid value | ignore it, log one warning per change, set the metric mode to `invalid`; never exit (the value changes at runtime and a crash loop would cut off all health reporting for the node) |

No Kubernetes API access is involved, so there are no `403` or watch-reconnect cases.

## Making it hard to forget

- A `WARN` log line when the override is applied, changed or removed, with the node, the value and
  the device count.
- Gauge `spyre_health_override{node, mode}`: one series set to 1, `mode` one of
  `none|healthy|unhealthy|invalid`.
- The per-device series carry `source="override"`.
- The admin guide suggests an alert on `mode="healthy"` lasting longer than a chosen time.

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
- **Per card**: entries listing PCI addresses.
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

- Value parsing: absent, both values in any case, surrounding whitespace and newline, empty, unknown.
- Apply: both modes over every state, PFs and VFs, `Source` and type kept, `REMOVED` unaffected.
- Order: `healthy` plus a missing driver gives `IN_ERROR` (a device that cannot exist in sysfs is
  used for this); pseudo devices are overridden like any other.
- File reader with a temporary directory: missing directory, missing file, unreadable file keeps the
  last value, file replaced by symlink swap (as kubelet does), invalid value reported.
- Startup: the first published state (from `NewServer`) already includes the override.
- Logging: the WARN line is written when the value changes, not on every check.
- Metric: one series, correct mode.

## Documentation to update with the code

`docs/reporters.md` and `docs/health-indicators.md` (the new step), `docs/deployment.md` (the volume),
`docs/configuration.md` (flag and environment variable), the README, and the admin guide in
`spyre-operator-docs` (how to create, change and remove an entry, the `healthy` warning, the alert
suggestion).
