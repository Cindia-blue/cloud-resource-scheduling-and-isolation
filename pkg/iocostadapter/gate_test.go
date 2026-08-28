package iocostadapter

import (
	"os"
	"path/filepath"
	"testing"
)

const gateTestUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func newGateTestRoot(t *testing.T) (root, cgroupPath string) {
	t.Helper()
	root = t.TempDir()
	cgroupPath = filepath.Join("kubepods.slice", "kubepods-besteffort.slice", "kubepods-besteffort-podaaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee.slice")
	if err := os.MkdirAll(filepath.Join(root, cgroupPath), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, cgroupPath
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGateCheck_NotReadyBeforeControllerState(t *testing.T) {
	root, cgroupPath := newGateTestRoot(t)
	writeFile(t, filepath.Join(root, cgroupPath, "io.weight"), "default 100\n")
	// No io.cost.model/io.cost.qos written at all yet.
	ok, reason := GateCheck(root, "259:0", gateTestUID, 100)
	if ok {
		t.Fatal("expected not ready before controller state exists")
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason")
	}
}

func TestGateCheck_NotReadyBeforeOwnOverrideWritten(t *testing.T) {
	root, cgroupPath := newGateTestRoot(t)
	writeFile(t, filepath.Join(root, "io.cost.model"), "259:0 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n")
	writeFile(t, filepath.Join(root, "io.cost.qos"), "259:0 enable=1 rpct=95.00 rlat=2500 wpct=95.00 wlat=5000 min=80.00 max=100.00\n")
	writeFile(t, filepath.Join(root, cgroupPath, "io.weight"), "default 100\n") // no override yet
	ok, reason := GateCheck(root, "259:0", gateTestUID, 100)
	if ok {
		t.Fatal("expected not ready before this Pod's own override is written")
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason")
	}
}

func TestGateCheck_NotReadyOnWrongWeight(t *testing.T) {
	root, cgroupPath := newGateTestRoot(t)
	writeFile(t, filepath.Join(root, "io.cost.model"), "259:0 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n")
	writeFile(t, filepath.Join(root, "io.cost.qos"), "259:0 enable=1 rpct=95.00 rlat=2500 wpct=95.00 wlat=5000 min=80.00 max=100.00\n")
	writeFile(t, filepath.Join(root, cgroupPath, "io.weight"), "default 100\n259:0 300\n")
	ok, _ := GateCheck(root, "259:0", gateTestUID, 100)
	if ok {
		t.Fatal("expected rejection when the materialized weight does not match expected")
	}
}

func TestGateCheck_ReadyOnceControllerAndOwnOverrideMatch(t *testing.T) {
	root, cgroupPath := newGateTestRoot(t)
	writeFile(t, filepath.Join(root, "io.cost.model"), "259:0 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n")
	writeFile(t, filepath.Join(root, "io.cost.qos"), "259:0 enable=1 rpct=95.00 rlat=2500 wpct=95.00 wlat=5000 min=80.00 max=100.00\n")
	writeFile(t, filepath.Join(root, cgroupPath, "io.weight"), "default 100\n259:0 100\n")
	ok, reason := GateCheck(root, "259:0", gateTestUID, 100)
	if !ok {
		t.Fatalf("expected ready, got not-ready reason: %s", reason)
	}
}

func TestGateCheck_UnresolvedPodCgroupFailsClosed(t *testing.T) {
	root := t.TempDir() // no matching cgroup directory at all
	writeFile(t, filepath.Join(root, "io.cost.model"), "259:0 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n")
	writeFile(t, filepath.Join(root, "io.cost.qos"), "259:0 enable=1 rpct=95.00 rlat=2500 wpct=95.00 wlat=5000 min=80.00 max=100.00\n")
	ok, reason := GateCheck(root, "259:0", gateTestUID, 100)
	if ok {
		t.Fatal("expected fail-closed when the Pod's cgroup cannot be resolved")
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason")
	}
}
