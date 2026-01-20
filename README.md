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

The server periodically runs health checks.
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
```
2025/12/09 17:53:07 using socket checker.sock
```

When running with the default pseudo devices (setting `export PSEUDO_DEVICE_MODE=1` before running the server), we 
would see the following client output:

```
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
```
I1209 17:58:20.713943   86070 main.go:47] loglevel: debug
I1209 17:58:20.714464   86070 main.go:50] Starting gRPC server
I1209 17:58:20.714786   86070 main.go:55] Starting timer for periodic checks
I1209 17:58:28.644194   86070 server.go:55] register health stream
I1209 17:58:28.644507   86070 server.go:90] update channel is not OK: rpc error: code = Canceled desc = context canceled
```

## Unit testing

Simply run: `make test`
