// gate-wait is the bounded workload-start barrier for one explicitly
// opted-in experiment Pod. It never touches Kubernetes, never depends on
// the iocost-adapter process being healthy, and performs only reads of the
// local cgroup filesystem (mounted read-only) -- so it fails closed on its
// own timeout even if the adapter itself has stalled or hung, which is
// exactly the failure mode this binary exists to make unmissable instead
// of silent (see docs/experiment-runbook.md and
// results/19-phase5-scheduler-selected-symmetric-100-100.md in the private
// workspace this fork's maintainer runs from).
//
// It writes the workload's own start-gate file only after independently
// proving, via pkg/iocostadapter.GateCheck, that the target device's
// controller state is ready and that THIS Pod's own resolved cgroup
// already carries the expected device-specific io.weight override. If that
// never becomes true within GATE_TIMEOUT, it exits non-zero without ever
// writing the start file, so a restartPolicy: Never Pod's fio container
// never starts.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	"sigs.k8s.io/IOIsolation/pkg/iocostadapter"
)

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
	podUID := os.Getenv("GATE_POD_UID")
	weightRaw := os.Getenv("GATE_EXPECTED_WEIGHT")
	startFile := env("GATE_START_FILE", "/gate/start")

	if device == "" || podUID == "" || weightRaw == "" {
		return fmt.Errorf("GATE_DEVICE, GATE_POD_UID, and GATE_EXPECTED_WEIGHT are all required")
	}
	weight64, err := strconv.ParseUint(weightRaw, 10, 32)
	if err != nil {
		return fmt.Errorf("GATE_EXPECTED_WEIGHT: %w", err)
	}
	timeout, err := time.ParseDuration(env("GATE_TIMEOUT", "60s"))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("GATE_TIMEOUT must be a positive duration")
	}
	poll, err := time.ParseDuration(env("GATE_POLL_INTERVAL", "1s"))
	if err != nil || poll < 100*time.Millisecond {
		return fmt.Errorf("GATE_POLL_INTERVAL must be at least 100ms")
	}

	deadline := time.Now().Add(timeout)
	var lastReason string
	for {
		ok, reason := iocostadapter.GateCheck(root, device, podUID, uint32(weight64))
		if ok {
			klog.Infof("gate-wait: materialization proven for Pod UID %s: %s", podUID, reason)
			if err := os.WriteFile(startFile, []byte("materialized\n"), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", startFile, err)
			}
			return nil
		}
		lastReason = reason
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for materialization for Pod UID %s: %s", timeout, podUID, lastReason)
		}
		time.Sleep(poll)
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
