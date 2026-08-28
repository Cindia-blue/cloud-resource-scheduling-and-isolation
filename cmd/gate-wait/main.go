// gate-wait is the bounded workload-start barrier for one bounded,
// exactly-two-application experiment cell. It never depends on the
// iocost-adapter process being healthy, and performs only reads of the
// local cgroup filesystem (mounted read-only) -- so it fails closed on
// its own timeout even if the adapter itself has stalled or hung, which
// is exactly the failure mode this binary exists to make unmissable
// instead of silent (see docs/experiment-runbook.md and
// results/19-phase5-scheduler-selected-symmetric-100-100.md in the
// private workspace this fork's maintainer runs from).
//
// It runs as its own standalone Pod, not an initContainer inside the
// application Pods: this repo's admission policy in the experiment
// namespace forces a user-namespace remap on privileged Pods that this
// binary's privileged read of /sys/fs/cgroup cannot satisfy, while the
// node-local namespace the adapter itself already runs in (see
// deployments/) is exempt -- the same reason the adapter is deployed
// there instead of alongside the application Pods. gate-wait is
// pinned to the same Node by nodeName, exactly like the adapter, and
// signals each application Pod via a host-path-backed start file that
// Pod mounts read-only via a plain, non-privileged hostPath volume (no
// admission conflict for a non-privileged mount).
//
// It writes each application's own start-gate file only after
// independently proving, via pkg/iocostadapter.GateCheck, that the
// target device's controller state is ready and that Pod's own resolved
// cgroup already carries the expected device-specific io.weight
// override. If that never becomes true for BOTH applications within
// GATE_TIMEOUT, it exits non-zero without writing either start file, so
// neither application's fio container ever starts.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	"sigs.k8s.io/IOIsolation/pkg/iocostadapter"
)

type target struct {
	name      string
	podUID    string
	weight    uint32
	startFile string
}

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()
	if err := run(); err != nil {
		klog.Fatalf("gate-wait: %v", err)
	}
}

func run() error {
	root := env("GATE_CGROUP_ROOT", "/sys/fs/cgroup")
	device := os.Getenv("GATE_DEVICE")
	if device == "" {
		return fmt.Errorf("GATE_DEVICE is required")
	}
	timeout, err := time.ParseDuration(env("GATE_TIMEOUT", "60s"))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("GATE_TIMEOUT must be a positive duration")
	}
	poll, err := time.ParseDuration(env("GATE_POLL_INTERVAL", "1s"))
	if err != nil || poll < 100*time.Millisecond {
		return fmt.Errorf("GATE_POLL_INTERVAL must be at least 100ms")
	}

	targets, err := loadTargets()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	pending := make(map[string]target, len(targets))
	for _, t := range targets {
		pending[t.name] = t
	}
	lastReasons := make(map[string]string, len(targets))

	for len(pending) > 0 {
		for name, t := range pending {
			ok, reason := iocostadapter.GateCheck(root, device, t.podUID, t.weight)
			if !ok {
				lastReasons[name] = reason
				continue
			}
			if err := os.WriteFile(t.startFile, []byte("materialized\n"), 0o644); err != nil {
				return fmt.Errorf("write %s for %s: %w", t.startFile, name, err)
			}
			klog.Infof("gate-wait: materialization proven for %s (Pod UID %s): %s", name, t.podUID, reason)
			delete(pending, name)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			var names []string
			for name := range pending {
				names = append(names, fmt.Sprintf("%s: %s", name, lastReasons[name]))
			}
			return fmt.Errorf("timed out after %s waiting for materialization: %v", timeout, names)
		}
		time.Sleep(poll)
	}
	return nil
}

// loadTargets reads exactly two numbered application targets (A and B),
// matching the fixed two-application shape pkg/iocostintent.Intent and
// the adapter's own reconcile() already require -- not a general-purpose
// N-target mechanism.
func loadTargets() ([]target, error) {
	var targets []target
	for _, suffix := range []string{"A", "B"} {
		podUID := os.Getenv("GATE_POD_UID_" + suffix)
		weightRaw := os.Getenv("GATE_EXPECTED_WEIGHT_" + suffix)
		startFile := os.Getenv("GATE_START_FILE_" + suffix)
		if podUID == "" || weightRaw == "" || startFile == "" {
			return nil, fmt.Errorf("GATE_POD_UID_%s, GATE_EXPECTED_WEIGHT_%s, and GATE_START_FILE_%s are all required", suffix, suffix, suffix)
		}
		weight64, err := strconv.ParseUint(weightRaw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("GATE_EXPECTED_WEIGHT_%s: %w", suffix, err)
		}
		targets = append(targets, target{name: suffix, podUID: podUID, weight: uint32(weight64), startFile: startFile})
	}
	return targets, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
