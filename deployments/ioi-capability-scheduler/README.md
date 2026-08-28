# Custom IOCost-capability scheduler

**Status: this is the validated Filter decision, distinct from the plain
stock scheduler in `../iocost-adapter/scheduler-optional/`.**

This directory packages a custom scheduler binary
(`scheduler-plugin/cmd/ioi-capability-scheduler`): stock `kube-scheduler`
command wiring plus exactly one added out-of-tree Filter plugin,
`IOCostCapability`. It is built against the `k8s.io/kubernetes v1.34.9`
dependency line as its own, separate Go module
(`scheduler-plugin/go.mod`) so it can match a specific cluster's control-
plane minor version without forcing every other package in this repo
(still on `v1.29.14`) to move with it. **Build it against the dependency
line matching your own cluster's control-plane minor version — a
mismatch is a real compatibility risk, not just a version-number
formality.**

Deploy exactly one of this scheduler or the plain optional scheduler at
a time — both use the `ioi-scheduler` profile name.

## What it proves

Only that an explicitly opted-in Pod (via
`spec.schedulerName: ioi-scheduler` and the existing
`cindy1.poc/io-weight` protection-intent annotation) is routed
exclusively onto a Node with an exact, current, unambiguous entry in a
dedicated capability inventory ConfigMap — not to any other candidate,
and not by `spec.nodeName`, `nodeAffinity`, or default-scheduler
fallback.

## What it does NOT prove

- Multi-node optimization, load balancing, or that a Score policy adds
  value (this plugin has no Score extension point).
- That `io.weight: 300` corresponds to any production priority level.
- A general noisy-neighbor prediction or automatic model-selection
  capability — the plugin never inspects or infers model coefficients;
  it only requires a `modelIdentity` field to be present.
- Production capability publication or model distribution: this bounded
  experiment's capability record is hand-built with
  `cmd/render-capability-record` from one target record at a time. How a
  real fleet would publish, version, and distribute qualification
  records to a scheduler at scale is unsolved and out of scope here (see
  `../../docs/limitations.md`).

## Contents

- `rbac.yaml`: ServiceAccount/ClusterRole/ClusterRoleBinding, scoped to
  standard scheduler Pod-binding/eventing plus **read-only** access to
  ConfigMaps (the one addition over the plain optional scheduler's RBAC).
  No cgroup, Node-mutation, webhook, NodePool, or EC2NodeClass
  permission of any kind.
- `scheduler-config.yaml`: `KubeSchedulerConfiguration` enabling exactly
  `PrioritySort`, `TaintToleration`, `NodeAffinity`, `NodeResourcesFit`,
  `IOCostCapability`, `DefaultBinder` — the same minimal profile as the
  plain optional scheduler, plus the one capability-aware Filter plugin.
- `scheduler-deployment.yaml.tmpl`: pin `{{SCHEDULER_IMAGE}}` to your own
  build's digest and `{{CAPABILITY_NAMESPACE}}` to the namespace holding
  the capability ConfigMap before applying.
- `capability-inventory-configmap.yaml.tmpl`: the dedicated, removable
  ConfigMap the plugin reads — see the comment inside for how to
  generate its contents from `cmd/render-capability-record`, never by
  hand-typing values.

See `../../docs/experiment-runbook.md` for the full qualify → apply →
test → cleanup procedure, and `../../docs/architecture.md` for how this
fits into the overall causal chain.
