# Indicators of Spyre card health

The overall health decision for each device is built in two stages.

## Stage 1 -- reporter pipeline

Each active reporter independently produces a `[]DeviceState` for the devices
it can observe. Results are merged by priority (see [Reporters](reporters.md)):

- **lspci**: device is `ONLINE` unless revision byte is `ff` (-> `IN_ERROR`).
- **cardmgmt**: device is `ONLINE` if the sidecar reports `"healthy"`, otherwise
  `IN_ERROR`.
- **ras**: device is `IN_ERROR` if a RAS hardware error was detected in a
  workload pod's logs on this node. This overrides any `ONLINE` from `lspci` or
  `cardmgmt` and cannot be cleared until the health-checker restarts.

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
