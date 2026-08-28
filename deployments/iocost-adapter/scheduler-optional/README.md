# Optional secondary scheduler

**Status: packaged, not validated by the completed causal experiment.**

This directory packages a stock, unmodified `kube-scheduler` binary as a
second, independently-run scheduler instance, opted into only by Pods
that set `spec.schedulerName: ioi-scheduler`. It never replaces, patches,
or restarts the cluster's default scheduler, and every Pod that doesn't
explicitly opt in is scheduled exactly as it always was.

It is included because it is a natural integration point for a future
placement/tenancy decision that wants to consume this project's IOCost
evidence (for example: "don't co-locate two protected workloads on the
same device"). **No such decision has been built or tested.** The
completed experiment (see `../../docs/architecture.md` and
`../../docs/experiment-runbook.md`) deliberately ran both of its causal
cells on one pre-qualified, pre-pinned node (`spec.nodeName` set
directly), which means:

- the scheduler in this directory was not the causal mechanism for
  anything measured;
- no scheduling, placement, or tenancy decision was exercised or
  validated;
- packaging this scheduler does not establish that its placement
  behavior works, is safe, or adds any value on top of the default
  scheduler.

Use it only as a starting point for building and independently
validating a real placement decision -- not as evidence that one already
exists.

## Contents

- `scheduler-config.yaml`: minimal `KubeSchedulerConfiguration` --
  disables every default plugin except `PrioritySort`, `TaintToleration`,
  `NodeAffinity`, `NodeResourcesFit`, and `DefaultBinder` (a bare
  Pod-to-node bind, no volume/topology/affinity/DRA plugins), to keep its
  RBAC footprint minimal.
- `scheduler-deployment.yaml`: single replica, no leader election,
  `--secure-port=0`, `DynamicResourceAllocation` feature gate disabled.
- `scheduler-rbac.yaml`: a distinct ServiceAccount/ClusterRole/
  ClusterRoleBinding scoped to scheduling only (`pods`, `pods/binding`,
  `nodes`, `events`, `namespaces` -- get/list/watch plus the writes a
  scheduler needs to bind and record events). Does not grant permission
  to modify the default scheduler or anything in `kube-system` beyond
  its own objects.
