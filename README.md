# spyre-health-checker

Health Checker for AIU Spyre Cards

> [!NOTE]
> This README is temporary, just to show this initial setup.

## Simple setup

This client-server system, using gRPC streaming, is the simplest implementation of what can be found in the `aiu-device-plugin:sunya-ch/auto-pilot-integration` branch. The server runs on `localhost:50051`.

The proto file comes from there. It can be edited and built via:

```bash
make protoc-gen
```

This project has a client and a server.

The server periodically runs health checks:

- By default the checker will call `lspci -vvvnn`
- If the user runs `export PSEUDO_DEVICE_MODE=1` before running the server, then a default set of pseudo devices are processed.

The periodic check timer can be set via command line parameter
in the format h-m-s, such as `5s` or `1h40m`.

## Build and run the server

First try linting:

```bash
make checks
```

Then to build the server:

```bash
go build -o spyre-health-checker ./cmd/health-checker/main.go
```

To run:

```bash
rm -f checker.sock # remove previously generated socket if exists
./spyre-health-checker --timer 5s --socket checker.sock
```

At one point the current server output looked like this:

```bash
I0826 21:32:03.349082   24618 main.go:36] loglevel: debug
I0826 21:32:03.350211   24618 main.go:37] Starting gRPC server
I0826 21:32:03.351596   24618 main.go:49] Starting timer for periodic checks
I0826 21:32:08.352772   24618 healthcheck.go:9] Running lspci
I0826 21:32:13.352538   24618 healthcheck.go:9] Running lspci
I0826 21:32:18.352445   24618 healthcheck.go:9] Running lspci
I0826 21:32:23.352272   24618 healthcheck.go:9] Running lspci
I0826 21:32:28.351234   24618 healthcheck.go:9] Running lspci
I0826 21:32:31.706218   24618 spyrehealthserver.go:49] [Server] Got a request
I0826 21:32:33.351071   24618 healthcheck.go:9] Running lspci
```

## Test with a client

To run a simple client in default mode, in another terminal, on a machine without Spyre cards:

```bash
go run cmd/client/client.go --socket=$(pwd)/checker.sock
```

All non-Spyre cards are ignored:

```text
2025/12/09 17:53:07 using socket checker.sock
```

When running with the default pseudo devices (setting `export PSEUDO_DEVICE_MODE=1` before running the server), we would see the following client output:

```text
2025/12/09 17:56:26 using socket checker.sock
2025/12/09 17:56:26   PCIAddress=0000:1a:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:1c:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:1d:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:1e:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:3d:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:3f:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:40:00.0  Type=PF  State=ONLINE
2025/12/09 17:56:26   PCIAddress=0000:41:00.0  Type=PF  State=IN_ERROR
```

Currently the server output for this case looks like this:

```text
I1209 17:58:20.713943   86070 main.go:47] loglevel: debug
I1209 17:58:20.714464   86070 main.go:50] Starting gRPC server
I1209 17:58:20.714786   86070 main.go:55] Starting timer for periodic checks
I1209 17:58:28.644194   86070 server.go:55] register health stream
I1209 17:58:28.644507   86070 server.go:90] update channel is not OK: rpc error: code = Canceled desc = context canceled
```

## Unit testing

Simply run: `make test`

## Detect-secrets usage

Detect Secrets tool will prevent your secrets from getting leaked. It is enabled by default as part of pre-commit hook, you just have to install it as below,

```sh
cd <repo path>
pre-commit install --install-hooks
```

With this, whenever changes are staged, detect-secrets hook will capture any leaked secret. However, if developer wants to scan repo on a routinely basis, follow the steps below:

### 1. Run detect-secrets hook

```sh
cd <repo path>
pre-commit run detect-secrets --all-files
```

This will catch any leaked-secret and fail the execution. If secrets are found, we have to scan and audit it as mentioned in following section.

### Detect-secrets cli

This is required when you want to update `.secrets.baseline` file with regards to any new secret.

#### 1. Install Detect Secrets

```sh
cd <repo path>
make detect-secrets-install
```

#### 2. Perform secret scan

```sh
cd <repo path>
make secrets-scan
```

Note: running the above command will create a .secrets.baseline file or update if already exists. Currently `go.sum` files are excluded from the scans.

#### 3. Audit secrets

```sh
cd <repo path>
make secrets-audit
```

Indicate (y)es if the secret found is an actual secret or (n)o if it is a false positive.
If any secrets are found in your audit, remove them from the code, revoke the access of the credentials, commit your changes, and repeat step 3. Once you have verified that all secrets are addressed, update `is_verified` field for different secrets to `true` in `.secrets.baseline` manually.

#### 4. Commit .secrets.baseline and push commit

You can repeat steps 2 to 4 every time you want to scan secrets.
