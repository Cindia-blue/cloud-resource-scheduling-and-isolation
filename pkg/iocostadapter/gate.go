package iocostadapter

import (
	"fmt"
	"path/filepath"

	"sigs.k8s.io/IOIsolation/pkg/iocostintent"
)

// GateCheck reports whether the workload-start barrier's condition
// currently holds for exactly one Pod: the target device has controller
// state ready (CheckControllerReady) AND this Pod's own resolved cgroup
// already shows a device-specific io.weight override equal to
// expectedWeight. It performs only reads, never a write, so it is safe to
// call repeatedly from a bounded poll loop.
//
// Checking only this Pod's own override is sufficient proof that BOTH
// applications in a cell were materialized together, not just this one:
// Materializer.Apply writes every planned weight and reads every one back
// before returning success, rolling back everything it applied if any
// single write or readback fails. A caller can therefore never observe a
// proven override from Apply's own successful return without every other
// write in the same plan having succeeded too.
func GateCheck(root, device, podUID string, expectedWeight uint32) (ok bool, reason string) {
	if err := CheckControllerReady(root, device, nil); err != nil {
		return false, err.Error()
	}
	cgroupPath, err := DiscoverPodCgroup(root, podUID)
	if err != nil {
		return false, err.Error()
	}
	content, err := OSFileIO{}.Read(filepath.Join(root, cgroupPath, "io.weight"))
	if err != nil {
		return false, fmt.Sprintf("read io.weight: %v", err)
	}
	weight, override, err := iocostintent.ParseDeviceWeight(content, device)
	if err != nil {
		return false, err.Error()
	}
	if !override {
		return false, "io.weight has no device-specific override yet"
	}
	if weight != expectedWeight {
		return false, fmt.Sprintf("io.weight override is %d, expected %d", weight, expectedWeight)
	}
	return true, "materialized: controller ready and this Pod's own io.weight override matches the expected value"
}
