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
5. Wait for both Pods to reach `Running`, then immediately `exec` a
   `touch /gate/start` into both.
6. Independently verify — from a separate `exec`/read path, not just the
   adapter's own emitted log — that both Pods' `io.weight` is the
   expected `100` on the exact `major:minor` device, and that the Pod
   UID → cgroup mapping matches what the adapter reports.
7. Poll for `/gate/done` in both Pods; retrieve `/gate/fio-result.json`
   from each as soon as it appears (a Pod can still hit
   `activeDeadlineSeconds` after `fio` finishes but before you collect
   the result, while it idles waiting for your stop signal — if that
   happens, the full JSON is usually still recoverable from
   `kubectl logs`, since the script tees to stdout as well).
8. `touch /gate/stop` in both Pods and confirm both reach `Completed`
   with exit code `0`.
9. **Full cleanup before the next cell**: delete both workload Pods,
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
