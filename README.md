# spyre-health-checker

Health Checker for AIU Spyre Cards

> [!NOTE]
> This README is temporary, just to show this initial setup.

## Simple setup

This client-server with gRPC streaming, is the simplest implementation of what can be found in the `aiu-device-plugin:sunya-ch/auto-pilot-integration` branch. The server runs on `localhost:50051`.

The proto file comes from there. It can be edited and built via

```bash
make protoc-gen
```

This project has a client and a server.

The server periodically run health checks. At the moment, the health check
is a fake call to `lspci`.
The periodic check timer can be set via command line parameter, can be any time
in the format h-m-s, for instance `5s` or `1h40m`.

## Build and run the server

To build the server:

```bash
go build -o spyre-health-checker ./cmd/health-checker/main.go
```

to run:

```bash
rm -f checker.sock # remove previously generated socket if exists
./spyre-health-checker --timer 5s --socket checker.sock
```

The current output looks like this

```bash
I0826 21:32:03.349082   24618 main.go:36] loglevel: debug
I0826 21:32:03.350211   24618 main.go:37] Starting gRPC server
I0826 21:32:03.351596   24618 main.go:49] Starting timer for periodic checks
I0826 21:32:08.352772   24618 healthcheck.go:9] Running lspci
I0826 21:32:13.352538   24618 healthcheck.go:9] Running lspci
I0826 21:32:18.352445   24618 healthcheck.go:9] Running lspci
I0826 21:32:23.352272   24618 healthcheck.go:9] Running lspci
I0826 21:32:28.351234   24618 healthcheck.go:9] Running lspci
I0826 21:32:31.706218   24618 spyrehalthserver.go:49] [Server] Got a request
I0826 21:32:33.351071   24618 healthcheck.go:9] Running lspci
```

## Test with a client

To run a simple client, in another terminal:

```bash
go run cmd/client/client.go --socket=$(pwd)/checker.sock
```

The client will receive the following dummy devices:

> 2025/08/28 18:24:30 using socket /Users/aa404681/Documents/internal_ws/aiu/spyre-health-checker/checker.sock
> 2025/08/28 18:24:30 Devices:
 [deviceID:{PCIAddress:"0000:1a:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:1c:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:1d:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:1e:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:3d:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:3f:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:40:00.0"} deviceType:PF deviceState:ONLINE deviceID:{PCIAddress:"0000:41:00.0"} deviceType:PF deviceState:IN_ERROR]
