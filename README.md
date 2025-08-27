# spyre-health-checker

Health Checker for AIU Spyre Cards

NOTE: this README is temporary, just to show this initial setup.

## Simple setup

This client-server with gRPC streaming, is the simplest implementation of what can be found in the `aiu-device-plugin:sunya-ch/auto-pilot-integration` branch. The server runs on `localhost:50051`.

The proto file comes from there. It can be edited and built via

```bash
cd pkg/proto/spyre_health
protoc --go_out=. --go_opt=paths=source_relative     --go-grpc_out=. --go-grpc_opt=paths=source_relative     spyre_health.proto
```

This project has a client and a server.

The server periodically run health checks. At the moment, the health check
is a fake call to `lspci`.
The periodic check timer can be set via command line parameter, can be any time
in the format h-m-s, for instance `5s` or `1h40m`.

## Build and run the server

To build the server:

```bash
go build -o spyre-health-checker ./pkg/main/main.go
```

to run:

```bash
./spyre-health-checker --timer 5s
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
cd client
go run client.go
```

The client will receive the following dummy devices:

```bash
2025/08/19 15:29:32 Devices:
 [deviceID:{PCIAddress:"00"}  deviceType:PF  deviceState:ONLINE deviceID:{PCIAddress:"01"}  deviceType:PF  deviceState:IN_ERROR]
```
