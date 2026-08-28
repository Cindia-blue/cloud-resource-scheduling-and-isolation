# Copyright (C) 2025 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

FROM golang:1.23-alpine AS builder

WORKDIR /workspace
COPY . .
RUN apk add --no-cache make
RUN CGO_ENABLED=0 make iocost-adapter gate-wait

FROM alpine:3.22
COPY --from=builder /workspace/bin/iocost-adapter /usr/local/bin/iocost-adapter
# gate-wait is the bounded, adapter-independent workload-start barrier run
# as an initContainer in the experiment Pod templates (see
# deployments/iocost-adapter/experiment/). Shipped in the same image as
# iocost-adapter since it is built from the same source tree and reuses
# pkg/iocostadapter directly -- not a separate release artifact.
COPY --from=builder /workspace/bin/gate-wait /usr/local/bin/gate-wait
ENTRYPOINT ["/usr/local/bin/iocost-adapter"]
