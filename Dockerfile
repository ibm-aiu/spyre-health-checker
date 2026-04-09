# +-------------------------------------------------------------------+
# | (C) Copyright IBM Corp. 2022-2026                                 |
# | SPDX-License-Identifier: Apache-2.0                               |
# +-------------------------------------------------------------------+

ARG BASE_UBI_IMAGE_TAG=9.6
ARG BUILDER_IMAGE
FROM ${BUILDER_IMAGE:-registry.access.redhat.com/ubi9/go-toolset:1.24.6-1758501173} AS builder
ARG TARGETOS
ARG TARGETARCH
USER root

WORKDIR /build
COPY go.mod .
COPY go.sum .
COPY vendor/ vendor/

COPY cmd/health-checker cmd/health-checker
COPY pkg/ pkg/
COPY internal/ internal/

# Build
ARG BUILD_FLAGS=""

ENV GOTOOLCHAIN="go1.24.13"

RUN echo "TARGETARCH = '${TARGETARCH}' TARGETOS='${TARGETOS}'" && \
    echo "GO ENV DUMP: " && go env GOVERSION && go env GOTOOLDIR && \
    CGO_ENABLED=1 GOOS=linux \
    go build ${BUILD_FLAGS} -mod vendor -tags strictfipsruntime -a -o spyre-health-checker ./cmd/health-checker

RUN dnf --installroot=/tmp/ubi-micro \
    --nodocs --setopt=install_weak_deps=False \
    install -y \
    pciutils openssl-libs openssl-fips-provider && \
    dnf --installroot=/tmp/ubi-micro \
    clean all

FROM registry.access.redhat.com/ubi9/ubi-micro:${BASE_UBI_IMAGE_TAG}

# Switch to non-root user (UID 1001) with root group (GID 0)
USER 1001:0

ARG VERSION
ARG RELEASE="N/A"

LABEL io.k8s.display-name="IBM Spyre Health Checker"
LABEL name="IBM Spyre Health Checker"
LABEL vendor="IBM"
LABEL version="${VERSION}"
LABEL release="N/A"
LABEL summary="Monitoring Basic health of IBM Spyre devices."
LABEL description="See summary"

COPY --from=builder /build/spyre-health-checker /usr/bin/spyre-health-checker
COPY --from=builder /tmp/ubi-micro/ /
COPY ./LICENSE /licenses/LICENSE

# Expose server HTTP health check port
EXPOSE 8080

# Expose metrics HTTP port
EXPOSE 8081

ENTRYPOINT [ "/usr/bin/spyre-health-checker" ]
HEALTHCHECK NONE
