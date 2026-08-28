# Experiment runbook: no-load qualification → symmetric → cleanup → differentiated → cleanup

This is the reusable procedure this fork's maintainer ran to produce the
"Demonstrated" evidence in [`limitations.md`](limitations.md). It assumes
a Kubernetes cluster with `containerd`/cgroup-v2, a node whose kernel
supports IOCost (`io.cost.model`/`io.cost.qos` present under
`/sys/fs/cgroup`), and cluster-admin access to create a privileged Pod.

Every step below is read-only or reversible, and every mutating step has
a matching rollback/cleanup step. **Do not skip the cleanup steps between
cells** — the differentiated cell must start from the same clean baseline
the symmetric cell did, on the same node/device, or the two cells are not
comparable.

## 0. Prerequisites

- Build/pull the `iocost-adapter` image (`cmd/iocost-adapter`) and pin it
  by digest.
- Confirm the source tree builds and its own invariant tests pass:
  `hack/validate-build.sh`.
- Confirm no manifest under `deployments/iocost-adapter` has a
  live-looking identifier or missing placeholder committed:
  `hack/scan-manifests.sh`.

## 1. Select and qualify one node

1. Pick one Ready, untainted node with no unrelated tenant workload and
   the block-device class you intend to test (this fork's own
   reproduction used `gp3, 16000 IOPS / 1000 MiB/s`, provisioned via a
   Karpenter `EC2NodeClass` — confirm your own device's class through
   whatever authoritative, read-only path your platform offers before
   trusting it; do not assume it from another node).
2. If your node provisioner can churn nodes out from under you
   (Karpenter consolidation/drift, cluster autoscaler, etc.), and it
   supports it, hold the node for the duration of the experiment with a
   minimal, non-privileged anchor Pod — for example, on Karpenter v1,
   a tiny Pod with `karpenter.sh/do-not-disrupt: "true"` blocks voluntary
   consolidation/drift while it runs. Confirm your provisioner's version
   actually supports this annotation before relying on it; do not cordon
   the node, patch its NodePool/disruption budget, or otherwise widen the
   change beyond one Pod's own annotation without separate authorization.
3. Build one `targetrecord.Record` (see `pkg/targetrecord`) from live,
   freshly-read Node/EBS data — never reuse a record collected for a
   different node or a previous run of this same node. Validate it with
   `targetrecord.Validate` and `targetrecord.MaxAge` before use (the
   `render-target` CLI does this for you).

## 2. Fresh qualification (no-load)

1. Render and apply `deployments/iocost-adapter/rbac/adapter-rbac.yaml`
   (once; idempotent) and a rendered
   `deployments/iocost-adapter/adapter-pod.yaml.tmpl` with `IOI_MODE=observe`.
2. Confirm the adapter's dynamic device resolution matches your own
   independent check (e.g. `stat` the kubelet data-root mount and derive
   major:minor yourself) — do not trust a single code path.
3. Confirm baseline: `io.cost.model`/`io.cost.qos` empty or already at a
   previously-accepted state, and every `kubepods*` `io.weight` at
   `default 100`.
4. Apply your reviewed `io.cost.model` and enable `io.cost.qos` for a
   short, explicitly bounded window (this fork's own reproduction used
   ≤10s for a true no-load qualification, with no Pod I/O running at
   all). Read back every field exactly. Disable QoS and confirm the
   disabled readback.
5. Tear down the qualification Pod and independently re-verify the
   baseline (model resident-but-inert is expected and does not require a
   reboot; QoS disabled; weights at default; no orphaned cgroups) from a
   **separate** Pod or session — not the one that performed the write.

If any of these checks fail or are ambiguous, stop. Do not proceed to a
workload cell on unqualified device state.

## 3. Symmetric `100:100` control cell

1. (Optional) render and apply the secondary-scheduler package
   (`deployments/iocost-adapter/scheduler-optional/`) if you want it
   present — it is not required for this procedure, since the workload
   Pods below are pinned by `spec.nodeName` (see the note in
   `architecture.md` on why this means the scheduler is not exercised).
2. Render `deployments/iocost-adapter/adapter-pod.yaml.tmpl` with
   `IOI_MODE=enforce`, `ACTIVE_CELL=symmetric-100-100`. Apply it.
3. Apply your reviewed `io.cost.model` to the qualified device and enable
   `io.cost.qos` — record the enable timestamp and enforce your own hard
   cap on how long it stays enabled (this fork used 120s as an operator-
   enforced ceiling for a real workload cell, tightened from the 10s
   no-load cap since a real `fio` run needs the window open for its
   whole runtime).
4. Render and apply
   `deployments/iocost-adapter/experiment/app-symmetric-control.yaml.tmpl`
   **immediately after** the adapter and device state are ready — do not
   let more than a few seconds elapse between Pod creation and releasing
   the start gate below, or `activeDeadlineSeconds` can fire before the
   workload ever runs.
5. Wait for both Pods to reach `Running`. **Do not open the gate yet.**
   The comment in `manifests/README.md` that "workload I/O cannot begin
   before adapter readback proves the treatment materialized" describes
   an intent, not an enforced invariant — nothing in the adapter or the
   Pod template checks this for you. You must check it yourself, every
   time:
   - Poll the adapter's `/readyz` (or its emitted log) until it reports
     `MATERIALIZED_READBACK_PROVEN` for this exact cell — not merely
     `Running`/`Ready` at the container level, and not merely the
     absence of a recent `FAIL_CLOSED` line (a stalled adapter can go
     silent without ever reaching either verdict; treat a poll timeout
     the same as an explicit failure).
   - Independently verify — from a separate `exec`/read path, not just
     the adapter's own emitted log — that both Pods' `io.weight` is
     already the expected value on the exact `major:minor` device, and
     that the Pod UID → cgroup mapping matches what the adapter reports.
   Only after both checks pass, `exec` a `touch /gate/start` into both
   Pods. If either check does not pass within your own bounded timeout,
   stop and clean up — do not open the gate on an unconfirmed adapter
   state, and do not infer materialization from workload throughput
   alone (a cell whose registered weight equals the cgroup-v2 default,
   such as a `100:100` symmetric control, cannot be distinguished from a
   complete absence of any write by throughput alone).
6. Poll for `/gate/done` in both Pods; retrieve `/gate/fio-result.json`
   from each as soon as it appears (a Pod can still hit
   `activeDeadlineSeconds` after `fio` finishes but before you collect
   the result, while it idles waiting for your stop signal — if that
   happens, the full JSON is usually still recoverable from
   `kubectl logs`, since the script tees to stdout as well).
7. `touch /gate/stop` in both Pods and confirm both reach `Completed`
   with exit code `0`.
8. **Full cleanup before the next cell**: delete both workload Pods,
   disable `io.cost.qos`, delete the adapter Pod, and independently
   verify (separate Pod/session) that weights are back to default, the
   model is inert, and no orphaned cgroups remain for either Pod's UID.

## 4. Differentiated `300:100` treatment cell

Repeat step 3 exactly, with these changes only:

- Render `app-differentiated-treatment.yaml.tmpl` instead of
  `app-symmetric-control.yaml.tmpl` (`app-a` requests weight `300`,
  `app-b` keeps `100`).
- `ACTIVE_CELL=differentiated-300-100`.
- Everything else — image, `fio` parameters, byte/rate/runtime caps,
  namespace, Pod identities, target device, and the evidence-collection
  procedure — must be identical to the symmetric cell, so the two are
  comparable.

Do not run this cell without a completed, cleaned-up symmetric cell on
the *same* node/device first — an isolated differentiated result with no
matched control is not a comparison.

## 5. Final cleanup

After the differentiated cell (or after any failure at any step):

1. Delete workload Pods.
2. Disable `io.cost.qos` and confirm the disabled readback.
3. Delete the adapter Pod (and the scheduler package, if you applied it).
4. Delete the anchor Pod, if one was used.
5. Independently verify: Node UID unchanged and healthy, weights at
   default everywhere, no orphaned cgroups for any Pod UID used across
   either cell, zero residual objects from this procedure.

## 6. Optional: capability-aware scheduler plumbing proof (separate from the cells above)

This procedure validates only the Filter-routing mechanism in
`scheduler-plugin/`/`deployments/ioi-capability-scheduler/` — it uses no
`io.weight`, no `fio`, and no live I/O, and is independent of sections
1-5. Do not mix it into the same cluster state as an active application
cell.

1. Build the scheduler image from `scheduler-plugin/` against the
   cluster's exact Kubernetes minor (the binary fails to start on a
   mismatch); apply `rbac.yaml`, `scheduler-config.yaml`, and
   `scheduler-deployment.yaml.tmpl` (rendered) to `kube-system`. RBAC
   must include read-only access to PersistentVolume/
   PersistentVolumeClaim/StorageClass/CSINode/CSIDriver/
   CSIStorageCapacity and `pods/status` patch — without these the
   scheduler framework's default informers never sync and no Pod is
   ever scheduled (this is a hard blocker, not log noise).
2. Generate the capability record with `cmd/render-capability-record`
   from a fresh, freshly-reverified target record — never hand-typed —
   and wrap it into the `ioi-capability-inventory` ConfigMap
   (`capability-inventory-configmap.yaml.tmpl` documents the exact
   `jq`/`kubectl` steps).
3. Run the four bounded tests against `test-pods/*.yaml.tmpl`: no
   record (Pod stays Pending, plugin reports missing qualification);
   one exact record (Pod binds to exactly that Node,
   `evaluatedNodes`/`feasibleNodes` in the scheduler's own logs
   confirms only one candidate passed); a deliberately stale Node UID
   in the record (all nodes rejected, reason names the exact
   stale-vs-live UID mismatch); a control Pod with no `schedulerName`
   (must follow the ordinary default scheduler, confirmed by the
   custom scheduler's own logs showing it never attempted to schedule
   or bind that Pod). Delete each test Pod and confirm `NotFound`
   before moving to the next test.
4. Final cleanup: delete the capability ConfigMap, the scheduler
   Deployment/ConfigMap/RBAC, and any anchor Pod; confirm zero residual
   objects and that all candidate Nodes are still `Ready`.

## Reading the results

- Treat PSI (`io.pressure`) as engagement/context evidence only, never as
  a standalone pass/fail signal.
- An `io.weight=100` readback is the *inherited default value* — it is
  only meaningful as a treatment when compared against the completed
  symmetric-cell baseline on the same node/device, not on its own.
- Do not expect or require a specific ratio between the treatment and
  control cells (see `limitations.md`).
- Do not claim scheduler/placement validation from this procedure — see
  `architecture.md`.
