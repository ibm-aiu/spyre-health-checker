# Configuration reference

All flags can also be set via the corresponding environment variable where noted.
Environment variables take effect if the flag is not explicitly supplied on the
command line.

## Flags and environment variables

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--socket` | -- | `/usr/local/etc/device-plugins/health/checker.sock` | Server UNIX socket path |
| `--timer` | -- | `1h` | Periodic health-check interval (duration format, e.g. `5s`, `1h40m`) |
| `--enabled-reporters` | -- | `lspci` | Comma-separated active reporters (`lspci`, `cardmgmt`) |
| `--health-port` | -- | `8080` | HTTP port for `/healthz` and `/readyz` probes |
| `--metrics-port` | -- | `8081` | HTTP port for Prometheus `/metrics` endpoint |
| `--tls-cert` | `SPYRE_TLS_CERT` | `/etc/spyre-health-checker/certs/tls.crt` | Server TLS certificate (also used as the mTLS client cert for the cardmgmt sidecar) |
| `--tls-key` | `SPYRE_TLS_KEY` | `/etc/spyre-health-checker/certs/tls.key` | Private key for `--tls-cert` |
| `--tls-ca` | `SPYRE_TLS_CA` | `/etc/spyre-health-checker/certs/ca.crt` | CA certificate used to verify both incoming clients (Channel A) and the cardmgmt sidecar (Channel B) |
| `--ras-watcher-limit-namespaces` | -- | *(empty -- all)* | Comma-separated trusted namespaces for the RAS watcher; set in multi-tenant clusters |
| `--cardhealth-socket` | `CARDHEALTH_GRPC_SOCKET` | `/var/run/cardmgmt-health-check-api/health-check-api.sock` | UNIX socket path of the `aiu-cardmgmt-health-api` sidecar |
| `--cardhealth-server-name` | `CARDHEALTH_TLS_SERVER_NAME` | `spyre-components` | TLS server name to verify in the cardmgmt sidecar certificate |
| `--debug` | -- | *(off)* | Enable per-slot gRPC call logging for the cardmgmt reporter (stdout) |

## Environment-only variables

| Variable | Default | Description |
|---|---|---|
| `PSEUDO_DEVICE_MODE` | *(unset)* | Set to `1` to replace hardware reporters with synthetic pseudo devices (see [Reporters -- Pseudo device mode](reporters.md#pseudo-device-mode)) |
| `NODE_NAME` | *(must be set in-cluster)* | Node name injected via the Kubernetes downward API; used by the RAS pod watcher to filter pods to the local node |

## Further reading

- [Reporters](reporters.md) -- details on each reporter and the flags that
  control them.
- [TLS reference](tls.md) -- full certificate requirements and troubleshooting.
- [Kubernetes deployment](deployment.md) -- Pod manifests and volume wiring.
