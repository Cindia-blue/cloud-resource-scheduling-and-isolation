# Copyright (C) 2025 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.23-alpine AS builder

WORKDIR /workspace
COPY . .
RUN apk add --no-cache make
RUN CGO_ENABLED=0 make iocost-adapter

FROM alpine:3.22
COPY --from=builder /workspace/bin/iocost-adapter /usr/local/bin/iocost-adapter
ENTRYPOINT ["/usr/local/bin/iocost-adapter"]
