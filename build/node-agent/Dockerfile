# Copyright (C) 2025 Intel Corporation
# SPDX-License-Identifier: Apache-2.0

# Stage 1: Build the Go binary
FROM golang:1.23-alpine as builder

WORKDIR /workspace
COPY . .
RUN apk add make
RUN make node-agent

# Stage 2: Final image
FROM alpine:3.17

ARG USER=ioi

WORKDIR /home/$USER
RUN apk add --no-cache fio sudo iproute2

RUN adduser -D $USER \
    && mkdir -p /etc/sudoers.d \
    && echo "$USER ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/$USER \
    && chmod 0440 /etc/sudoers.d/$USER

# Copy binary
COPY --chown=$USER --from=builder /workspace/bin/node-agent /bin/node-agent

# Copy config
COPY --chown=$USER --from=builder /workspace/deployments/config.toml /etc/ioi/config/

# ✅ NEW: Copy wrapper script
COPY --chown=$USER ./build/node-agent/run.sh /usr/local/bin/run-node-agent.sh
RUN chmod +x /usr/local/bin/run-node-agent.sh

WORKDIR /bin
USER root

# ✅ NEW ENTRYPOINT: ensure kubeconfig is passed
ENTRYPOINT ["/usr/local/bin/run-node-agent.sh"]
