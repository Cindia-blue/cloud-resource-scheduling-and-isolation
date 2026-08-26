// Package iocostintent defines the NOP-safe application protection contract.
//
// The historical project expressed bandwidth requests and enforced them with
// io.max.  This package deliberately keeps the reusable intent shape while
// replacing that retired enforcement primitive with IOCost io.weight.  It is
// pure planning code: it never opens cgroup files or calls Kubernetes.
package iocostintent

import (
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	MinWeight = 1
	MaxWeight = 10000
)

var devicePattern = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

// ApplicationBinding binds a logical application to one exact Pod generation
// and the Pod-level cgroup observed on the target node.
type ApplicationBinding struct {
	Application string
	PodUID      string
	CgroupPath  string // relative to /sys/fs/cgroup
}

// Intent contains only application-to-application differentiation.  The root
// io.cost.model and io.cost.qos treatment is a separate node-level contract.
type Intent struct {
	Device string

	Protected       ApplicationBinding
	Competing       ApplicationBinding
	ProtectedWeight uint32
	CompetingWeight uint32
	BaselineWeight  uint32
}

type PlannedWrite struct {
	Application string
	PodUID      string
	CgroupPath  string
	File        string
	Value       string
}

type Plan struct {
	Treatment []PlannedWrite
	Rollback  []PlannedWrite
}

func Validate(intent Intent) error {
	if !devicePattern.MatchString(intent.Device) {
		return fmt.Errorf("invalid device major:minor %q", intent.Device)
	}
	if err := validateBinding("protected", intent.Protected); err != nil {
		return err
	}
	if err := validateBinding("competing", intent.Competing); err != nil {
		return err
	}
	if intent.Protected.PodUID == intent.Competing.PodUID {
		return fmt.Errorf("applications reuse Pod UID %q", intent.Protected.PodUID)
	}
	if cleanCgroup(intent.Protected.CgroupPath) == cleanCgroup(intent.Competing.CgroupPath) {
		return fmt.Errorf("applications share cgroup %q", intent.Protected.CgroupPath)
	}
	for name, weight := range map[string]uint32{
		"protected": intent.ProtectedWeight,
		"competing": intent.CompetingWeight,
		"baseline":  intent.BaselineWeight,
	} {
		if weight < MinWeight || weight > MaxWeight {
			return fmt.Errorf("%s weight %d outside cgroup-v2 range %d..%d", name, weight, MinWeight, MaxWeight)
		}
	}
	return nil
}

func validateBinding(role string, binding ApplicationBinding) error {
	if strings.TrimSpace(binding.Application) == "" || strings.TrimSpace(binding.PodUID) == "" {
		return fmt.Errorf("%s identity is incomplete", role)
	}
	clean := cleanCgroup(binding.CgroupPath)
	if clean == "." || filepath.IsAbs(binding.CgroupPath) || strings.HasPrefix(clean, "..") {
		return fmt.Errorf("%s cgroup path %q is not a safe relative path", role, binding.CgroupPath)
	}
	if !strings.HasPrefix(clean, "kubepods.slice/") {
		return fmt.Errorf("%s cgroup path %q is outside kubepods.slice", role, binding.CgroupPath)
	}
	uidToken := strings.ReplaceAll(binding.PodUID, "-", "_")
	if !strings.Contains(clean, uidToken) {
		return fmt.Errorf("%s cgroup path does not contain Pod UID generation", role)
	}
	return nil
}

func cleanCgroup(path string) string { return filepath.ToSlash(filepath.Clean(path)) }

// BuildPlan emits io.weight writes only. It can never produce io.max,
// io.cost.model, or io.cost.qos writes.
func BuildPlan(intent Intent) (Plan, error) {
	if err := Validate(intent); err != nil {
		return Plan{}, err
	}
	return Plan{
		Treatment: []PlannedWrite{
			planned(intent.Device, intent.Protected, intent.ProtectedWeight),
			planned(intent.Device, intent.Competing, intent.CompetingWeight),
		},
		Rollback: []PlannedWrite{
			planned(intent.Device, intent.Protected, intent.BaselineWeight),
			planned(intent.Device, intent.Competing, intent.BaselineWeight),
		},
	}, nil
}

func planned(device string, binding ApplicationBinding, weight uint32) PlannedWrite {
	return PlannedWrite{
		Application: binding.Application,
		PodUID:      binding.PodUID,
		CgroupPath:  cleanCgroup(binding.CgroupPath),
		File:        "io.weight",
		Value:       fmt.Sprintf("%s %d", device, weight),
	}
}

// ParseDeviceWeight returns the effective weight and whether a device-specific
// override was present. The default weight is used only when no override exists.
func ParseDeviceWeight(content, device string) (uint32, bool, error) {
	var defaultWeight uint32 = 100
	for scanner := bufio.NewScanner(strings.NewReader(content)); scanner.Scan(); {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			return 0, false, fmt.Errorf("invalid io.weight line %q: %w", scanner.Text(), err)
		}
		if fields[0] == device {
			return uint32(value), true, nil
		}
		if fields[0] == "default" {
			defaultWeight = uint32(value)
		}
	}
	return defaultWeight, false, nil
}
