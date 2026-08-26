package iocostadapter

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeDeviceStat struct{ majMin map[string]string }

func (f fakeDeviceStat) Resolve(path string) (string, error) {
	v, ok := f.majMin[path]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}

// writeWholeDisk registers majMin as a whole disk (no "partition" marker) at
// /sys/dev/block/<majMin>.
func writeWholeDisk(t *testing.T, sysRoot, majMin string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "dev", "block", majMin)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// writePartition registers childMajMin as a partition of a whole disk whose
// own device number is parentMajMin, mirroring the real
// /sys/dev/block/<child> -> /sys/devices/.../<disk>/<disk><N> symlink and the
// disk directory's own "dev" file.
func writePartition(t *testing.T, sysRoot, childMajMin, parentMajMin string) {
	t.Helper()
	diskDir := filepath.Join(sysRoot, "devices", "disk-"+parentMajMin)
	partDir := filepath.Join(diskDir, "part")
	if err := os.MkdirAll(partDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partDir, "partition"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diskDir, "dev"), []byte(parentMajMin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(sysRoot, "dev", "block")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(partDir, filepath.Join(linkDir, childMajMin)); err != nil {
		t.Fatal(err)
	}
}

// Matches this node's live topology: data mount -> nvme1n1 (259:0, whole
// disk, no partition), root mount -> nvme0n1p1 (259:2, partition of 259:1).
func TestResolveDataDevice_MatchesObservedTopology(t *testing.T) {
	sysRoot := t.TempDir()
	writeWholeDisk(t, sysRoot, "259:0")
	writePartition(t, sysRoot, "259:2", "259:1")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "259:0",
		"/":            "259:2",
	}}
	got, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "259:0" {
		t.Fatalf("got %q, want 259:0", got)
	}
}

// A different node may assign entirely different major:minor pairs; the
// resolver must re-derive them locally on every run, never assume a
// fleet-wide constant.
func TestResolveDataDevice_NotPortableAcrossNodes(t *testing.T) {
	sysRoot := t.TempDir()
	writeWholeDisk(t, sysRoot, "8:0")
	writePartition(t, sysRoot, "259:1", "259:0")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "8:0",
		"/":            "259:1",
	}}
	got, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8:0" {
		t.Fatalf("got %q, want 8:0 (device numbers must not be treated as a fleet-wide constant)", got)
	}
}

func TestResolveDataDevice_PartitionResolvesToParentDisk(t *testing.T) {
	sysRoot := t.TempDir()
	writePartition(t, sysRoot, "259:5", "259:0")
	writePartition(t, sysRoot, "259:2", "259:1")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "259:5",
		"/":            "259:2",
	}}
	got, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "259:0" {
		t.Fatalf("data partition 259:5 must resolve to its parent disk 259:0, got %q", got)
	}
}

func TestResolveDataDevice_RejectsRootVolume(t *testing.T) {
	sysRoot := t.TempDir()
	writePartition(t, sysRoot, "259:2", "259:1")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "259:2",
		"/":            "259:2",
	}}
	if _, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/"); err == nil {
		t.Fatal("expected fail-closed rejection when the data mount resolves to the root device")
	}
}

func TestResolveDataDevice_MissingMountFailsClosed(t *testing.T) {
	sysRoot := t.TempDir()
	ds := fakeDeviceStat{majMin: map[string]string{
		"/": "259:2",
	}}
	if _, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/"); err == nil {
		t.Fatal("expected fail-closed rejection when the data mount cannot be resolved")
	}
}

func TestResolveDataDevice_AmbiguousMajMinFailsClosed(t *testing.T) {
	sysRoot := t.TempDir()
	writePartition(t, sysRoot, "259:2", "259:1")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "not-a-device",
		"/":            "259:2",
	}}
	if _, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/"); err == nil {
		t.Fatal("expected fail-closed rejection for a malformed major:minor")
	}
}

func TestResolveDataDevice_MissingSysfsEntryFailsClosed(t *testing.T) {
	sysRoot := t.TempDir()
	writePartition(t, sysRoot, "259:2", "259:1")
	ds := fakeDeviceStat{majMin: map[string]string{
		"/mnt/kubelet": "259:0",
		"/":            "259:2",
	}}
	if _, err := ResolveDataDevice(ds, sysRoot, "/mnt/kubelet", "/"); err == nil {
		t.Fatal("expected fail-closed rejection when the whole-disk sysfs entry is absent")
	}
}
