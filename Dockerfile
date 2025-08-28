# Copyright (c) 2022 IBM Corp. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

ARG BASE_UBI_IMAGE_TAG=9.6
ARG BUILDER_IMAGE
FROM ${BUILDER_IMAGE:-registry.access.redhat.com/ubi9/go-toolset:9.6-1754467841} AS builder
ARG TARGETOS
ARG TARGETARCH
USER root

WORKDIR /build
COPY go.mod .
COPY go.sum .
COPY vendor/ vendor/

COPY cmd/ cmd/
COPY pkg/ pkg/
COPY internal/ internal/

# Build
ARG BUILD_FLAGS=""
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
