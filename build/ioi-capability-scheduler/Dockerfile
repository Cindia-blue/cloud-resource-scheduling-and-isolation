# Build context: scheduler-plugin/ (a standalone Go module, deliberately
# separate from the repo root module so it can pin an exact Kubernetes
# v1.34.9 dependency line without touching every other package's
# v1.29.14 pin -- see docs/architecture.md).
FROM golang:1.24-alpine AS builder

WORKDIR /workspace
COPY . .
RUN CGO_ENABLED=0 go build -o /workspace/bin/ioi-capability-scheduler ./cmd/ioi-capability-scheduler

FROM alpine:3.22
COPY --from=builder /workspace/bin/ioi-capability-scheduler /usr/local/bin/ioi-capability-scheduler
ENTRYPOINT ["/usr/local/bin/ioi-capability-scheduler"]
