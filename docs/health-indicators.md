# Indicators of Spyre card health

The overall health decision for each device is built in three stages. The last one is optional.

## Stage 1 -- reporter pipeline

Each active reporter independently produces a `[]DeviceState` for the devices
it can observe. Results are merged by priority (see [Reporters](reporters.md)):

- **lspci**: device is `ONLINE` unless revision byte is `ff` (-> `IN_ERROR`).
- **cardmgmt**: device is `ONLINE` if the sidecar reports `"healthy"` (sidecar 0.0.4 and
  earlier) or `"online"` (0.0.5 and later). Other statuses of sidecar 0.0.5 and later map to the
  closest state: `OFFLINE` for `offline`, `BOOTING` for `starting`, `pfcheck` and `pfbooted`,
  `SHUTTING_DOWN` for `pf-quiescing`, and `IN_ERROR` for `failed`; unknown values are
  `DEVICE_STATE_UNSPECIFIED`. `na` (no entry in card management) is no verdict: the other reporters
  decide for the slot, unless `CARDHEALTH_STRICT_NA=true` makes it `UNSPECIFIED`
  (see [Reporters](reporters.md#cardmgmt-reporter)).
  VFs take the state of their PF.
- **ras**: device is `IN_ERROR` if a RAS hardware error was detected in a
  workload pod's logs on this node. This overrides any `ONLINE` from `lspci` or
  `cardmgmt` and cannot be cleared until the health-checker restarts.

### UNSPECIFIED is not IN_ERROR

`UNSPECIFIED` means that the state of a device is not known: card management returned a status that this version does
not know, or `na` while `CARDHEALTH_STRICT_NA=true`. It is not a finding that the device is faulty. `IN_ERROR` is a
detector reporting a fault.

For scheduling they are the same: the device plugin and the DRA driver offer only `ONLINE` devices. They differ in the
metric, where `spyre_device_state` carries `state="UNSPECIFIED"` or `state="IN_ERROR"`. An alert on devices that are not
available should therefore use `spyre_device_state{state!="ONLINE"} == 1`; `state="IN_ERROR"` alone does not match
`UNSPECIFIED`.

## Stage 2 -- driver check

After merging reporter results, `internal/healthcheck/healthcheck.go` verifies
the kernel driver symlink for every non-pseudo device:

```
/sys/bus/pci/devices/<pciAddress>/driver
```

If `os.Stat()` fails or does not complete within 5 seconds, the device is forced
to `DEVICE_STATE_IN_ERROR` regardless of what the reporters returned. This
catches cards that are visible to `lspci` and the sidecar but have lost their
kernel driver binding.

## Stage 3 -- node health override (optional)

An administrator can force the result for every device of a node with an entry in the optional
ConfigMap `spyre-health-override`: the key is the node name and the value is `healthy` or `unhealthy`
(see [Node health override](deployment.md#node-health-override)). The override is applied after
Stage 2, to every device the reporters returned:

| Value | Effect |
|---|---|
| `unhealthy` | every device is `IN_ERROR` |
| `healthy` | every device is `ONLINE`, except a device that failed the driver check, which stays `IN_ERROR` |
| anything else | ignored: the result of Stages 1 and 2 is kept, and the metric mode is `invalid` |

The override is unconditional: it has no expiry and ignores what the reporters say, now or later.
With `healthy`, keep in mind that:

- it hides a RAS error but does not clear it, so the error is reported again when the entry is removed;
- it can make workloads use a card that is not up (for example `OFFLINE` or `BOOTING`) or that has a
  real fault.

A forced device has `source="override"` in the metrics. With no ConfigMap, no entry for the node, or no
mount, nothing changes. The design is in
[`enhancements/node-health-override.md`](../enhancements/node-health-override.md).

## lspci filter rules (Stage 1 detail)

Before classifying devices, the lspci reporter applies these filters:

- Devices where vendor:device ID is neither `1014:06a7` (PF) nor `1014:06a8`
  (VF) are excluded.
- Devices where revision (`REV`) is not `01` are excluded (rev `01` is a
  known-good boot state for Spyre).
- Revision `ff` -> `DEVICE_STATE_IN_ERROR`.

## Possible future indicators

The following are not currently implemented but could be considered:

- Power state not `D0` -> offline.
- `SERR+`, `TAbort+`, `MAbort+`, or `FatalErr+` set -> error.
- Kernel driver path from `lspci` output validated for correctness.
