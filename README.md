# spyre-health-checker

Health Checker for AIU Spyre Cards.

This client-server system uses gRPC streaming and is a simplified implementation
of the health checking found in
[spyre-device-plugin](https://github.com/ibm-aiu/spyre-device-plugin).

The server runs on a UNIX socket (default
`/usr/local/etc/device-plugins/health/checker.sock`) with mTLS. It periodically
runs health checks using a pluggable reporter framework and streams device state
updates to connected clients.

The periodic check timer can be set via the `--timer` flag in duration format,
such as `5s` or `1h40m` (default `1h`).

## Requirements

- `lspci` must be available on the host when using the `lspci` reporter.
- Access to the `aiu-cardmgmt-health-api` UNIX socket is required when the
  `cardmgmt` reporter is enabled.
- A Kubernetes `ClusterRole` granting `pods` and `pods/log` access is required
  for the RAS pod watcher (see [Kubernetes deployment](docs/deployment.md#rbac)).

## Documentation

| Topic | Description |
|---|---|
| [Reporters](docs/reporters.md) | Reporter framework, merge rules, lspci / cardmgmt / RAS / pseudo device mode |
| [TLS reference](docs/tls.md) | Channel A & B overview, certificate requirements, troubleshooting |
| [Kubernetes deployment](docs/deployment.md) | RBAC, socket wiring, Pod manifests |
| [Configuration reference](docs/configuration.md) | All flags and environment variables |
| [Development guide](docs/development.md) | Building, testing, protobuf generation |
| [Health indicators](docs/health-indicators.md) | How card health is determined (lspci filters, driver check) |

Detailed design documents are in [`enhancements/`](enhancements/).

## Quick start

```bash
# Install tools and vendor dependencies
make ginkgo envtest vendor

# Generate local dev TLS certs (once per clone)
make gen-local-certs

# Run the test suite
make test

# Build the binary
go build -o spyre-health-checker ./cmd/health-checker/
```


## Repository links

- [Contributing](CONTRIBUTING.md)
- [Maintainers](MAINTAINERS.md)
- [License](LICENSE)
