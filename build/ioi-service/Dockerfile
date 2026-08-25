# Copyright (C) 2025 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.23-alpine AS builder

WORKDIR /workspace
COPY . .
RUN apk add --no-cache make clang llvm libbpf-dev linux-headers
RUN make ioi-service

FROM alpine:3.22
RUN apk add --no-cache util-linux
COPY --from=builder /workspace/bin/ioi-service /usr/local/bin/ioi-service
COPY deployments/config.toml /etc/ioi/config/config.toml
ENTRYPOINT ["/usr/local/bin/ioi-service"]
