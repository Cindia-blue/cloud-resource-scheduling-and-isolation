// Package targetrecord fixes the render-time identity of one disposable
// experiment node so that manifests never embed a permanent EC2 instance ID,
// and every live phase can fail closed if that node has been replaced.
//
// Major:minor is deliberately excluded from the identity comparison: it is
// node-local kernel numbering, not a portable device identity (see
// pkg/iocostadapter/devicemap.go), so a device/volume mismatch is proven by
// EBS volume ID, not by major:minor equality.
package targetrecord

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Record is the one authoritative, render-time snapshot of a chosen
// experiment node's identity and data-device identity. It is generated
// fresh for each qualification/run attempt and is never committed with a
// resolved instance ID.
type Record struct {
	ClusterID          string
	Context            string
	NodeName           string
	NodeUID            string
	ProviderID         string
	InstanceID         string
	DataVolumeID       string
	NVMeDevice         string
	DeviceMajMin       string
	EBSType            string
	EBSIOPS            int
	EBSThroughputMiBps int
	CollectedAt        string // RFC3339
}

var placeholderPattern = regexp.MustCompile(`__[A-Z0-9_]+__`)

// Validate rejects a Record that is incomplete, contains an unresolved
// render placeholder, or carries an unparseable collection timestamp. It
// does not check liveness -- that is ReconfirmIdentity's job.
func Validate(r Record) error {
	fields := map[string]string{
		"ClusterID":    r.ClusterID,
		"Context":      r.Context,
		"NodeName":     r.NodeName,
		"NodeUID":      r.NodeUID,
		"ProviderID":   r.ProviderID,
		"InstanceID":   r.InstanceID,
		"DataVolumeID": r.DataVolumeID,
		"NVMeDevice":   r.NVMeDevice,
		"DeviceMajMin": r.DeviceMajMin,
		"EBSType":      r.EBSType,
		"CollectedAt":  r.CollectedAt,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("target record field %s is empty", name)
		}
		if placeholderPattern.MatchString(value) {
			return fmt.Errorf("target record field %s contains an unresolved placeholder: %q", name, value)
		}
	}
	if r.EBSIOPS <= 0 {
		return fmt.Errorf("target record EBSIOPS must be positive, got %d", r.EBSIOPS)
	}
	if r.EBSThroughputMiBps <= 0 {
		return fmt.Errorf("target record EBSThroughputMiBps must be positive, got %d", r.EBSThroughputMiBps)
	}
	if _, err := time.Parse(time.RFC3339, r.CollectedAt); err != nil {
		return fmt.Errorf("target record CollectedAt is not RFC3339: %w", err)
	}
	return nil
}

// MaxAge fails closed if the record was collected longer ago than max,
// relative to now. Callers pass now explicitly so this stays deterministic
// under test.
func MaxAge(r Record, now time.Time, max time.Duration) error {
	collected, err := time.Parse(time.RFC3339, r.CollectedAt)
	if err != nil {
		return fmt.Errorf("target record CollectedAt is not RFC3339: %w", err)
	}
	if age := now.Sub(collected); age > max {
		return fmt.Errorf("target record is stale: collected %s ago, max allowed %s", age, max)
	}
	return nil
}

// LiveNodeIdentity is what must be re-read from the live API server
// immediately before every apply.
type LiveNodeIdentity struct {
	Found      bool
	NodeUID    string
	ProviderID string
}

// ReconfirmIdentity fails closed unless the live Node is present and its UID
// and providerID exactly match the frozen record. A Node with the same name
// but a different UID (deleted and recreated, or replaced by Karpenter and
// happening to reuse a name) is rejected, not silently adopted.
func ReconfirmIdentity(r Record, live LiveNodeIdentity) error {
	if !live.Found {
		return fmt.Errorf("node %s not found: target node has been deleted", r.NodeName)
	}
	if live.NodeUID == "" {
		return fmt.Errorf("live Node UID is empty for %s", r.NodeName)
	}
	if live.NodeUID != r.NodeUID {
		return fmt.Errorf("node %s UID changed: record has %s, live is %s (node was replaced under the same name)", r.NodeName, r.NodeUID, live.NodeUID)
	}
	if live.ProviderID == "" {
		return fmt.Errorf("live providerID is empty for %s", r.NodeName)
	}
	if live.ProviderID != r.ProviderID {
		return fmt.Errorf("node %s providerID changed: record has %s, live is %s", r.NodeName, r.ProviderID, live.ProviderID)
	}
	return nil
}

// LiveDeviceIdentity is what must be re-read from the node (via SSH and a
// read-only EBS query) immediately before every apply.
type LiveDeviceIdentity struct {
	DataVolumeID       string
	DeviceMajMin       string
	EBSType            string
	EBSIOPS            int
	EBSThroughputMiBps int
}

// ReconfirmDevice fails closed unless the live data device exactly matches
// the frozen record's EBS volume ID, type, and performance class. MajMin is
// compared too (it must match the same record), but a MajMin match alone
// with a different VolumeID is never sufficient -- MajMin is not portable
// identity.
func ReconfirmDevice(r Record, live LiveDeviceIdentity) error {
	if live.DataVolumeID == "" {
		return fmt.Errorf("live data volume ID is empty")
	}
	if live.DataVolumeID != r.DataVolumeID {
		return fmt.Errorf("data volume mismatch: record has %s, live is %s", r.DataVolumeID, live.DataVolumeID)
	}
	if live.DeviceMajMin != r.DeviceMajMin {
		return fmt.Errorf("device major:minor mismatch for volume %s: record has %s, live is %s", r.DataVolumeID, r.DeviceMajMin, live.DeviceMajMin)
	}
	if live.EBSType != r.EBSType {
		return fmt.Errorf("EBS type mismatch for volume %s: record has %s, live is %s", r.DataVolumeID, r.EBSType, live.EBSType)
	}
	if live.EBSIOPS != r.EBSIOPS {
		return fmt.Errorf("EBS IOPS mismatch for volume %s: record has %d, live is %d", r.DataVolumeID, r.EBSIOPS, live.EBSIOPS)
	}
	if live.EBSThroughputMiBps != r.EBSThroughputMiBps {
		return fmt.Errorf("EBS throughput mismatch for volume %s: record has %d MiB/s, live is %d MiB/s", r.DataVolumeID, r.EBSThroughputMiBps, live.EBSThroughputMiBps)
	}
	return nil
}

// Placeholders returns the __KEY__ substitution map for RenderPlaceholders.
func (r Record) Placeholders() map[string]string {
	return map[string]string{
		"__CLUSTER_ID__":           r.ClusterID,
		"__CONTEXT__":              r.Context,
		"__NODE_NAME__":            r.NodeName,
		"__NODE_UID__":             r.NodeUID,
		"__PROVIDER_ID__":          r.ProviderID,
		"__INSTANCE_ID__":          r.InstanceID,
		"__DATA_VOLUME_ID__":       r.DataVolumeID,
		"__NVME_DEVICE__":          r.NVMeDevice,
		"__DEVICE_MAJMIN__":        r.DeviceMajMin,
		"__EBS_TYPE__":             r.EBSType,
		"__EBS_IOPS__":             fmt.Sprintf("%d", r.EBSIOPS),
		"__EBS_THROUGHPUT_MIBPS__": fmt.Sprintf("%d", r.EBSThroughputMiBps),
		"__COLLECTED_AT__":         r.CollectedAt,
	}
}

// RenderPlaceholders substitutes every __KEY__ token in template with the
// record's fields and fails closed if any __...__-shaped token remains
// afterward -- a typo'd or unmapped placeholder must never render silently
// as literal text into a manifest that gets applied.
func RenderPlaceholders(template string, r Record) (string, error) {
	out := template
	for key, value := range r.Placeholders() {
		out = strings.ReplaceAll(out, key, value)
	}
	if m := placeholderPattern.FindString(out); m != "" {
		return "", fmt.Errorf("unresolved placeholder remains after render: %s", m)
	}
	return out, nil
}
