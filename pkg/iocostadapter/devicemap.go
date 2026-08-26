package iocostadapter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// DeviceStat isolates the block-device-identity boundary for deterministic
// tests. Resolve returns the "major:minor" of the block device backing the
// filesystem that contains path.
type DeviceStat interface {
	Resolve(path string) (majMin string, err error)
}

// OSDeviceStat stats the real filesystem, the same primitive `stat` uses.
type OSDeviceStat struct{}

func (OSDeviceStat) Resolve(path string) (string, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	dev := uint64(st.Dev)
	return fmt.Sprintf("%d:%d", unix.Major(dev), unix.Minor(dev)), nil
}

// ResolveDataDevice resolves the node-local whole-disk major:minor backing
// dataMountPath, and fails closed if that device is not distinguishable from
// the node's root device.
//
// Steps: dataMountPath -> device number of its filesystem (stat) -> if that
// device is a partition, its parent whole disk's own device number (sysfs)
// -> reject if the result equals the root device's whole-disk major:minor.
func ResolveDataDevice(ds DeviceStat, sysRoot, dataMountPath, rootMountPath string) (string, error) {
	if ds == nil {
		return "", fmt.Errorf("no device-stat resolver configured")
	}
	dataMajMin, err := ds.Resolve(dataMountPath)
	if err != nil {
		return "", fmt.Errorf("resolve data mount %s: %w", dataMountPath, err)
	}
	rootMajMin, err := ds.Resolve(rootMountPath)
	if err != nil {
		return "", fmt.Errorf("resolve root mount %s: %w", rootMountPath, err)
	}

	dataDisk, err := wholeDiskMajMin(sysRoot, dataMajMin)
	if err != nil {
		return "", fmt.Errorf("data device %s: %w", dataMajMin, err)
	}
	rootDisk, err := wholeDiskMajMin(sysRoot, rootMajMin)
	if err != nil {
		return "", fmt.Errorf("root device %s: %w", rootMajMin, err)
	}
	if dataDisk == rootDisk {
		return "", fmt.Errorf("fail closed: data mount %s resolves to the root device %s, not a distinct data volume", dataMountPath, dataDisk)
	}
	return dataDisk, nil
}

// wholeDiskMajMin resolves a device's own major:minor to its parent whole
// disk's major:minor. A device with no /sys/dev/block/<majMin>/partition
// marker is already a whole disk and is returned unchanged; a partition's
// major:minor never matches its parent disk's and must not be used as the
// disk identity.
func wholeDiskMajMin(sysRoot, majMin string) (string, error) {
	if !validMajMin(majMin) {
		return "", fmt.Errorf("empty or malformed major:minor %q", majMin)
	}
	// sysRoot is conventionally /sys; /sys/dev/block/<majMin> is the kernel's
	// own major:minor -> device index, used here instead of trusting a name.
	devDir := filepath.Join(sysRoot, "dev", "block", majMin)
	if _, err := os.Stat(devDir); err != nil {
		return "", fmt.Errorf("stat %s: %w", devDir, err)
	}
	partitionMarker := filepath.Join(devDir, "partition")
	if _, err := os.Stat(partitionMarker); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat %s: %w", partitionMarker, err)
		}
		// Not a partition: this device is already the whole disk.
		return majMin, nil
	}
	// devDir is a symlink into /sys/devices/.../<disk>/<partition>; resolve
	// it before walking up, so ".." lands on the parent disk's own sysfs
	// directory rather than the literal parent of the symlink path.
	realDevDir, err := filepath.EvalSymlinks(devDir)
	if err != nil {
		return "", fmt.Errorf("resolve symlink %s: %w", devDir, err)
	}
	parentDev := filepath.Join(realDevDir, "..", "dev")
	f, err := os.Open(parentDev)
	if err != nil {
		return "", fmt.Errorf("open parent disk dev file for partition %s: %w", majMin, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "", fmt.Errorf("empty parent disk dev file for partition %s", majMin)
	}
	parent := strings.TrimSpace(scanner.Text())
	if !validMajMin(parent) {
		return "", fmt.Errorf("malformed parent major:minor %q for partition %s", parent, majMin)
	}
	if parent == majMin {
		return "", fmt.Errorf("ambiguous mapping: partition %s reports itself as its own parent disk", majMin)
	}
	return parent, nil
}

func validMajMin(majMin string) bool {
	parts := strings.SplitN(majMin, ":", 2)
	if len(parts) != 2 {
		return false
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return false
	}
	return true
}
