# NOP IOCost application isolation: installation and operations manual

This manual covers the disk-only, IOCost-based application I/O
differentiation mechanism maintained on the `cindy1-nop-iocost` branch of
this fork. For what this mechanism has and has not been shown to do, read
[`limitations.md`](limitations.md) first. For the component boundaries
and the validated causal chain, see [`architecture.md`](architecture.md).
For a step-by-step reproduction, see
[`experiment-runbook.md`](experiment-runbook.md).

## What this is

A node-local adapter that materializes an application's declared I/O
weight intent into cgroup-v2 `io.weight`, on top of a device that already
has a reviewed `io.cost.model`/`io.cost.qos` state. It replaces the
historical `io.max` hard-throttle path entirely (hard-disabled in this
fork) with the kernel's IOCost work-conserving controller.

## What this is not

A production-ready application-protection product, or a validated
scheduler placement/tenancy mechanism. See
[`limitations.md`](limitations.md) for the exact maturity boundaries.

## Prerequisites

- Kubernetes with `containerd` and cgroup-v2.
- A node kernel with the IOCost controller available
  (`io.cost.model`/`io.cost.qos` present under `/sys/fs/cgroup`).
- Cluster-admin access sufficient to create one privileged Pod
  (the adapter needs `securityContext.privileged: true` and host mounts
  of `/sys/fs/cgroup`, the kubelet data root, `/`, and `/sys` — it does
  **not** need `hostPID`, `hostNetwork`, or `hostIPC`).
- Go toolchain matching `go.mod`, to build `cmd/iocost-adapter` and
  `cmd/render-target`.

## Install

1. Build and pin the adapter image by digest:
   ```
   go build -o bin/iocost-adapter ./cmd/iocost-adapter
   go build -o bin/render-target ./cmd/render-target
   # package bin/iocost-adapter into a container image and push it,
   # then pin the resulting digest in your rendered manifest.
   ```
2. Apply the adapter's RBAC once:
   ```
   kubectl apply -f deployments/iocost-adapter/rbac/adapter-rbac.yaml
   ```
3. For every attempt: collect a fresh `targetrecord.Record` for your
   target node (see `pkg/targetrecord`), then render
   `deployments/iocost-adapter/adapter-pod.yaml.tmpl` against it with
   `cmd/render-target`:
   ```
   ./bin/render-target \
     -record target-record.json \
     -template deployments/iocost-adapter/adapter-pod.yaml.tmpl \
     -out rendered/
   kubectl apply -f rendered/adapter-pod.yaml
   ```
4. (Optional) apply the secondary-scheduler package
   (`deployments/iocost-adapter/scheduler-optional/`) only if you intend
   to build and validate a real placement decision on top of it — it is
   not required for the adapter itself to work, and is not exercised by
   the reference experiment cells (see `architecture.md`).

## Operate

- **Observe mode** (`IOI_MODE=observe`, the template default): read-only
  reconciliation. The adapter reports what it sees; it writes nothing.
- **Enforce mode** (`IOI_MODE=enforce` + `ACTIVE_CELL=<name>`): the
  adapter watches `TARGET_NAMESPACE` for exactly two `Running` Pods
  sharing that cell's label and carrying the `cindy1.poc/io-weight`
  annotation, and materializes/reads back their weights. It refuses to
  proceed (fails closed, does not report Ready) if the target device
  lacks a reviewed `io.cost.model`/`io.cost.qos` state, or if it finds
  anything other than exactly two eligible Pods sharing one cell.
- Deleting the adapter Pod triggers its own shutdown rollback of any
  weight it applied, if the target Pods still exist. It does **not** roll
  back device-level `io.cost.model`/`io.cost.qos` — that is a separate,
  deliberate step (see the runbook) so a device-level qualification can
  outlive one adapter Pod's lifecycle.

See [`experiment-runbook.md`](experiment-runbook.md) for the full
qualify → symmetric → cleanup → differentiated → cleanup procedure,
including the bounded QoS-enabled-window cap and gate-release timing this
fork's own reproduction depends on.

## Uninstall / cleanup

1. Delete any workload Pods carrying the experiment's cell labels.
2. Disable `io.cost.qos` on the target device (`enable=0`) and confirm
   the readback.
3. Delete the adapter Pod.
4. Delete the optional scheduler package, if applied.
5. Independently verify (a separate Pod or session from whichever one
   performed the write): weights back to `default 100`, no orphaned
   per-Pod cgroups, target Node healthy.

`io.cost.model` may remain resident on the device but inert once
`io.cost.qos` is disabled — this is expected and does not require a
reboot to "fully" clean up, per the runbook's own accepted rollback
state.

## Upstream / attribution

This fork descends from the original
[intel/IOIsolation](https://github.com/intel/IOIsolation) project, which
Intel discontinued (see the top of the repository `README.md`). This
fork's disk-only, IOCost-based mechanism is a from-scratch replacement of
that project's historical `io.max` disk-bandwidth enforcement path and
its scheduler/network/RDT scope, not a continuation of Intel's
maintenance or support. Report issues against this fork, not upstream.
