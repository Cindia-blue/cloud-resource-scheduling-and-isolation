# Architecture: component boundaries and the validated causal chain

This document describes the disk-only, IOCost-based fork of IOIsolation
maintained on the `cindy1-nop-iocost` branch, what each component does,
and — critically — exactly what has and has not been causally validated
by the completed experiment. See [`experiment-runbook.md`](experiment-runbook.md)
for the reproducible procedure and [`limitations.md`](limitations.md)
for the maturity-level breakdown this document is built on.

## Components

| Component | Path | Role |
|---|---|---|
| `iocost-adapter` | `cmd/iocost-adapter`, `pkg/iocostadapter` | The **sole** writer of Pod-level cgroup-v2 `io.weight`. Resolves the node's data device dynamically (never a hardcoded major:minor — see `pkg/iocostadapter/devicemap.go`), fails closed if the device has no reviewed `io.cost.model`/`io.cost.qos` state, and materializes/reads back/rolls back exactly the two Pods carrying one active cell label. |
| `iocostintent` | `pkg/iocostintent` | Pure planning code for the application-to-application weight intent (`Intent`, `Plan`, `PlannedWrite`). Never opens a cgroup file or calls Kubernetes itself. |
| `targetrecord` | `pkg/targetrecord`, `cmd/render-target` | Fail-closed identity binding: one frozen, render-time snapshot of Node identity (name/UID/providerID) and device identity (EBS volume ID/type/IOPS/throughput/major:minor), re-verified immediately before every live apply. Rejects cross-node, stale, unresolved, or duplicate mappings. Major:minor is never treated as portable across nodes. |
| optional secondary scheduler | `deployments/iocost-adapter/scheduler-optional/` | A stock, unmodified `kube-scheduler` binary, opt-in only via `spec.schedulerName`. **Packaged, not validated** — see below. |
| capability-aware secondary scheduler | `scheduler-plugin/`, `deployments/ioi-capability-scheduler/` | A custom `kube-scheduler` binary with exactly one added Filter plugin (`IOCostCapability`), built as its own Go module against the cluster's exact Kubernetes minor. Opt-in only via `spec.schedulerName: ioi-scheduler` plus a protection-intent annotation. **Bounded scheduler-plumbing proof completed — see below. Still not multi-node optimization, a Score policy, or a production placement decision.** |
| retired `io.max` path | `pkg/service/disk` | The historical hard-throttle enforcement primitive. Hard-disabled in this fork (`codes.Unimplemented`); IOCost `io.weight` replaces it entirely for the disk-only scope this fork maintains. |

## The validated causal chain

```
application intent → IOCost adapter → device/cgroup materialization
  → differentiated outcome + antagonist cost → readback + recovery
```

1. **Application intent**: a Pod declares `cindy1.poc/io-weight` (see
   `deployments/iocost-adapter/experiment/*.yaml.tmpl`) and a shared
   `cindy1.poc/cell` label identifying it as one of exactly two Pods in
   one bounded comparison.
2. **IOCost adapter**: a privileged, single-node adapter Pod
   (`deployments/iocost-adapter/adapter-pod.yaml.tmpl`) discovers the
   two eligible Pods, resolves each one's cgroup path from its Pod UID,
   and writes the intended `io.weight` to each — after independently
   confirming the target device already has a reviewed
   `io.cost.model`/`io.cost.qos` state (it will not act otherwise).
3. **Device/cgroup materialization**: every write is read back from the
   live cgroup file, not assumed from the write call succeeding.
4. **Differentiated outcome + antagonist cost**: measured via a bounded,
   byte/rate/runtime-capped `fio` pair — one Pod's benefit is measured
   together with the other's cost, in the same run, on the same device.
5. **Readback + recovery**: after every cell, QoS is disabled, weights
   are restored to the accepted baseline, and recovery is proven by an
   independent read path (a separate Pod/session from whichever one
   performed the write).

This chain has been demonstrated end-to-end, including a bounded
`100:100` symmetric control and a `300:100` differentiated treatment, on
one pre-qualified node per run. See `experiment-runbook.md` for the exact
procedure and the private results this fork's maintainer collected while
running it.

## What is explicitly NOT validated: scheduler placement

The optional secondary scheduler in
`deployments/iocost-adapter/scheduler-optional/` is a legitimate,
minimal-footprint integration point for a *future* placement or tenancy
decision that wants to consume this project's IOCost evidence. **It is
not that decision, and running the experiment above does not validate
it.**

Specifically: the completed causal cells pin both application Pods with
`spec.nodeName` set directly in the rendered manifest (a node-rotation-
safety property — see `experiment-runbook.md`), not via the optional
scheduler's placement logic. A Pod with `nodeName` already set at
creation is bound by the kubelet directly; **no scheduler, including the
one packaged here, ever performs a binding action on such a Pod.**
Therefore:

- the secondary scheduler was not the causal mechanism for anything this
  experiment measured;
- no scheduling, placement, or tenancy decision was exercised, let alone
  validated;
- the EKS/Kubernetes default scheduler was never modified or bypassed;
- packaging the secondary scheduler does not by itself establish that
  its placement behavior works, is safe, or adds value over the default
  scheduler.

The unvalidated chain this leaves open is:

```
application intent → scheduler placement/tenancy decision
```

See `limitations.md` for how this fits into the project's overall
maturity, and `scheduler-optional/README.md` for the component's own
scope note.

## What IS now validated: bounded scheduler plumbing (separate from the IOCost chain above)

A distinct, later phase validated one narrow claim about the
capability-aware secondary scheduler
(`scheduler-plugin/`, `deployments/ioi-capability-scheduler/`) on a
3-node topology (1 pre-qualified, 2 unqualified): an opted-in Pod with
no `nodeName`/affinity is routed exclusively onto a Node with an exact,
current, unambiguous entry in a dedicated capability inventory
ConfigMap — not any other candidate, and not by `nodeName`, affinity, or
default-scheduler fallback. Evidence (four live tests, each followed by
full cleanup):

- **No record**: Pod stays `Pending`; plugin reports "missing
  qualification" for every real candidate; no fallback.
- **One exact qualified record**: `evaluatedNodes=5 feasibleNodes=1` —
  the plugin's own Filter binds the Pod to exactly the qualified Node.
- **Stale Node UID**: all nodes rejected, including the previously-
  qualified one, with a rejection reason naming the exact stale-vs-live
  UID mismatch; Pod stays `Pending`.
- **Default-scheduler control** (no `schedulerName`): the Pod is placed
  by the ordinary EKS default scheduler; the custom scheduler's own logs
  show it never attempted to schedule or bind that Pod.

This is still **not** the same claim as the IOCost causal chain above —
the two are independent proofs and neither one validates the other. The
scheduler-plumbing proof does not use `io.weight`, `fio`, or any live
I/O; the IOCost chain's completed cells still use `spec.nodeName`, not
this scheduler. What remains open: multi-node optimization, a Score
policy, `io.weight` value semantics, and production capability
publication/model distribution (see `limitations.md`).
