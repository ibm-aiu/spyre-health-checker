## Contributing in General
Our project welcomes external contributions. If you have an itch, please feel
free to scratch it.

To contribute code or documentation, please submit a [pull request](https://github.com/ibm-aiu/spyre-health-checker/pulls).

A good way to familiarize yourself with the codebase and contribution process is
to look for and tackle low-hanging fruit in the [issue tracker](https://github.com/ibm-aiu/spyre-health-checker/issues).

**Note: We appreciate your effort, and want to avoid a situation where a contribution
requires extensive rework (by you or by us), sits in backlog for a long time, or
cannot be accepted at all!**

### Proposing new features

If you would like to implement a new feature, please [raise an issue](https://github.com/ibm-aiu/spyre-health-checker/issues)
before sending a pull request so the feature can be discussed. This is to avoid
you wasting your valuable time working on a feature that the project developers
are not interested in accepting into the code base.

### Fixing bugs

If you would like to fix a bug, please [raise an issue](https://github.com/ibm-aiu/spyre-health-checker/issues) before sending a
pull request so it can be tracked.

### Merge approval

The project maintainers use LGTM (Looks Good To Me) in comments on the code
review to indicate acceptance. A change requires LGTMs from two of the
maintainers of each component affected.

For a list of the maintainers, see the [MAINTAINERS.md](MAINTAINERS.md) page.

### Development Guide

#### Prerequisite

- Go 1.24.13 or later (see `go.mod` for current version)
- Operator SDK 1.38
- Kubernetes cluster and its client (`kubectl`, `oc`, etc.) for test-purpose
- OpenShift Local for running end to end tests on your local workstation (laptop)
- podman or docker cli tools
- pre-commit

#### Image build

```sh
make docker-build
```

> [!NOTE]
> The default docker image built is `docker.io/spyre-operator/spyre-health-checker`.
> To push the image to remote registry, the remote registry must be set as follow:
>
>```sh
> REGISTRY=[your-remote-registry] make docker-build-push
>```

#### Pre-commit hook

Pre-commit hooks help maintain code quality by running automated checks before each commit. The hooks are configured in `.pre-commit-config.yaml` and include:

- Code formatting (go-fmt, yamlfmt, shell-fmt)
- Linting (golangci-lint, codespell)
- Security checks (detect-secrets, detect-private-key)
- File validation (check-json, check-yaml, etc.)

##### Installation

Install pre-commit hooks:

```sh
pre-commit install --install-hooks
```

##### Manual execution

To run all hooks manually:

```sh
pre-commit run --all-files
```

##### Detect Secrets

The detect-secrets tool prevents secrets from being committed to the repository. It runs automatically as part of the pre-commit hook.

###### Install Detect Secrets CLI

To manually work with the `.secrets.baseline` file:

```sh
make detect-secrets-install
```

###### Scan for secrets

Create or update the `.secrets.baseline` file:

```sh
make secrets-scan
```

> [!NOTE]
> This command excludes `go.sum` files from scans.

###### Audit secrets

Review and classify detected secrets:

```sh
make secrets-audit
```

For each detected secret:
- Press `y` if it's an actual secret (then remove it from code and revoke credentials)
- Press `n` if it's a false positive

After addressing all secrets, manually update the `is_verified` field to `true` in `.secrets.baseline`.

###### Commit changes

After auditing, commit the updated `.secrets.baseline` file:

```sh
git add .secrets.baseline
git commit -m "chore: update secrets baseline"
```

Repeat the scan and audit process as needed when adding new code.

## Legal

Each source file must include a license header for the Apache
Software License 2.0. Using the SPDX format is the simplest approach.
e.g.

```
/*
(C) Copyright IBM Corp. 2025, 2026
SPDX-License-Identifier: Apache-2.0
*/
```

We have tried to make it as easy as possible to make contributions. This
applies to how we handle the legal aspects of contribution. We use the
same approach - the [Developer's Certificate of Origin 1.1 (DCO)](https://github.com/hyperledger/fabric/blob/master/docs/source/DCO1.1.txt) - that the Linux® Kernel [community](https://elinux.org/Developer_Certificate_Of_Origin)
uses to manage code contributions.

We simply ask that when submitting a patch for review, the developer
must include a sign-off statement in the commit message.

Here is an example Signed-off-by line, which indicates that the
submitter accepts the DCO:

```text
Signed-off-by: John Doe <john.doe@example.com>
```

You can include this automatically when you commit a change to your
local git repository using the following command:

```sh
git commit -s
```

## Communication

Not available yet.

## Setup

1. Install [Go](https://go.dev/doc/install)

    > Please check the project `go.mod` file for current Go version.

2. Download dependencies

```sh
sudo apt-get update
sudo apt-get install -y bc unzip curl ca-certificates uuid-runtime
```

3. Install [pre-commit](https://pre-commit.com/)

## Unit Test

```sh
make test
```

## Build and run server by-hand

It is often necessary to build and run the server by hand during development.

To build:

```sh
go build -o spyre-health-checker ./cmd/health-checker/main.go
```

To run:

```sh
rm -f checker.sock # remove previously generated socket if exists
./spyre-health-checker --timer 5s --socket checker.sock
```

Server output would look something like this:

```console
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

### Test with a client

To run a simple client in default mode, in another terminal, on a machine without Spyre cards:

```sh
go run cmd/client/client.go --socket=$(pwd)/checker.sock
```

All non-Spyre cards are ignored:

```console
2025/12/09 17:53:07 using socket checker.sock
```

When running with the default pseudo devices (setting `export PSEUDO_DEVICE_MODE=1` before running the server), we would see the following client output:


```console
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

Server output for this case looks like this:

```console
I1209 17:58:20.713943   86070 main.go:47] loglevel: debug
I1209 17:58:20.714464   86070 main.go:50] Starting gRPC server
I1209 17:58:20.714786   86070 main.go:55] Starting timer for periodic checks
I1209 17:58:28.644194   86070 server.go:55] register health stream
I1209 17:58:28.644507   86070 server.go:90] update channel is not OK: rpc error: code = Canceled desc = context canceled
```
