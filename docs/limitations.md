# Maturity: demonstrated, packaged, and open

This fork's disk-only IOCost work sits at three distinct maturity levels.
Do not read a claim at one level as if it applied to another.

## Demonstrated

IOCost application-intent materialization, directional synthetic
differentiation, benefit/cost measurement, and recovery — **on one
pre-qualified node, per run**:

- Dynamic, fail-closed resolution of the node's data device (never a
  hardcoded major:minor).
- A device-level `io.cost.model`/`io.cost.qos` no-load materialization
  and rollback, with exact-field readback at every step.
- A bounded symmetric `100:100` control cell and a bounded differentiated
  `300:100` treatment cell, run back-to-back on the same node/device,
  with full cleanup and independently re-verified recovery between and
  after both.
- Directional differentiation: in the maintainer's own reproduction, the
  `300`-weighted application showed higher throughput and lower mean/tail
  latency than the `100`-weighted one in the same cell, while the
  `100`-weighted application's own throughput/latency degraded relative
  to its performance in the symmetric control. **No specific ratio (e.g.
  3:1) is claimed or should be expected** — the actual ratio depends on
  the device, the workload shape, and the kernel's IOCost controller
  behavior, and only one class of device/workload has been tested.
- Independent recovery: QoS disablement, restored default weights, no
  orphaned cgroups, and healthy node state, verified via a separate read
  path from whichever session performed the write.

This is real, repeatable, synthetic-workload evidence of the causal
chain in `architecture.md`. It is not a benchmark of any specific
production workload, and no cost/benefit envelope from it has been
reviewed or approved as acceptable for any real application.

## Packaged but not validated

The optional secondary scheduler
(`deployments/iocost-adapter/scheduler-optional/`) is packaged as a
minimal, opt-in integration extension point for a future
placement/tenancy decision. It has not been exercised by the completed
experiment: both application Pods in every completed cell were pinned
with `spec.nodeName` directly, which bypasses scheduler binding
entirely. The scheduler in this repo:

- has not been shown to place Pods correctly, safely, or usefully;
- has not been tested for interaction with node churn, disruption, or
  multi-node placement decisions;
- should be treated as example code for a future validation effort, not
  as a working feature.

Keep it in the repo as a starting point — do not remove it — but do not
describe it as validated, tested, or production-ready in any downstream
documentation.

## Open product gate

Two things this fork does **not** provide, and that would need real
design and validation work before any production use:

1. **A real workload protection envelope.** What weight ratio, QoS
   percentile/latency target, and antagonist-cost tolerance are
   acceptable for a *specific* real application, under *its* real I/O
   shape and SLOs — not the synthetic `fio` shape used here. No such
   envelope has been proposed, reviewed, or approved by anyone.
2. **A real placement/tenancy decision that consumes this evidence.**
   Something that decides, at scheduling time, which workloads may
   safely share a device given their protection intent — built on top
   of (but distinct from) the optional scheduler package above, and
   validated the same rigorous way the IOCost materialization chain was:
   with real causal experiments, not by inference from packaging.

Until both of these exist and are independently reviewed, this project
should be described as a working proof of the IOCost materialization
mechanism — not as an application-protection product.
