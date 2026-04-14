# spyre-health-checker

Health Checker for AIU Spyre Cards

## Requirements

The health checker requires the `lspci` command to gather information on Spyre cards.

## Simple setup

This client-server system, using gRPC streaming, is a simplified implementation of that found in `https://github.com/ibm-aiu/spyre-device-plugin`.
The server runs on `localhost:50051`.

The proto file comes from there. It can be edited and built via:

```sh
make protoc-gen
```

This project has a client and a server.

The server periodically runs health checks:

- By default the checker will call `lspci -vvvnn`
- If the user runs `export PSEUDO_DEVICE_MODE=1` before running the server, then a default set of pseudo devices are processed.

The periodic check timer can be set via command line parameter
in the format h-m-s, such as `5s` or `1h40m`.

## Indicators of Spyre Card Health

Indicators of Spyre card health include the following:
- First, according to `lspci -vvvnn` output, the following pci devices are filtered out:
    - devices where vendor:device ID for the device is neither `1014:06a7` nor `1014:06a8`.
    - devices where 'REV' is not `01`.
- If `REV` is `ff` then set device status to `DEVICE_STATE_IN_ERROR`
- Finally, if `os.Stat()` for the device driver (e.g., `/sys/bus/pci/devices/<PCI Address>/driver` fails for any reason, including timeout, set the device status to `DEVICE_STATE_IN_ERROR`

Although not implemented at the moment the following could considered for future development:

- If state is not `D0`, the card could be considered to be offline.
- If flags such as `SERR+`, `TAbort+`, `MAbort+` or `FatalErr+` are set, the card could be considered to be in an error state.
- The existence of a correct driver in the location reported by `lspci` could be considered.

## License
This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
