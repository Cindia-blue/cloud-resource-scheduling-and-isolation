// Package iocostadapter materializes a bounded application-level IOCost
// intent into cgroup-v2 io.weight and verifies the effective state.
package iocostadapter

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sigs.k8s.io/IOIsolation/pkg/iocostintent"
)

// CheckControllerReady proves that the target device has a user-supplied
// linear cost model and IOCost QoS is enabled. Per-cgroup weights are not a
// meaningful IOCost treatment without this node-level state.
func CheckControllerReady(root, device string, io CgroupIO) error {
	if io == nil {
		io = OSFileIO{}
	}
	model, err := io.Read(filepath.Join(root, "io.cost.model"))
	if err != nil {
		return fmt.Errorf("read io.cost.model: %w", err)
	}
	qos, err := io.Read(filepath.Join(root, "io.cost.qos"))
	if err != nil {
		return fmt.Errorf("read io.cost.qos: %w", err)
	}
	modelLine := deviceLine(model, device)
	qosLine := deviceLine(qos, device)
	if modelLine == "" {
		return fmt.Errorf("target device has no io.cost.model")
	}
	modelFields := attributes(modelLine)
	if modelFields["ctrl"] != "user" || modelFields["model"] != "linear" {
		return fmt.Errorf("target device does not use a user-supplied linear model")
	}
	for _, field := range []string{"rbps", "rseqiops", "rrandiops", "wbps", "wseqiops", "wrandiops"} {
		value, err := strconv.ParseUint(modelFields[field], 10, 64)
		if err != nil || value == 0 {
			return fmt.Errorf("target model field %s must be a positive integer", field)
		}
	}
	qosFields := attributes(qosLine)
	if qosLine == "" || qosFields["enable"] != "1" {
		return fmt.Errorf("target device IOCost QoS is not enabled")
	}
	return nil
}

func attributes(line string) map[string]string {
	result := make(map[string]string)
	for _, field := range strings.Fields(line) {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func deviceLine(content, device string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, device+" ") {
			return line
		}
	}
	return ""
}

// CgroupIO isolates the cgroup filesystem boundary for deterministic tests.
type CgroupIO interface {
	Read(path string) (string, error)
	Write(path, value string) error
}

type OSFileIO struct{}

func (OSFileIO) Read(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func (OSFileIO) Write(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(value)
	return err
}

type EffectiveWeight struct {
	Application string
	PodUID      string
	CgroupPath  string
	Weight      uint32
	Override    bool
}

type Materializer struct {
	Root     string
	Device   string
	Baseline uint32
	IO       CgroupIO
}

func (m Materializer) Apply(plan iocostintent.Plan) ([]EffectiveWeight, error) {
	if m.IO == nil {
		m.IO = OSFileIO{}
	}
	if len(plan.Treatment) == 0 {
		return nil, fmt.Errorf("empty treatment plan")
	}

	// The bounded PoC requires a clean inherited baseline. An explicit
	// device override, even with the same numeric value, is treated as stale
	// state and blocks a new cell.
	for _, write := range plan.Treatment {
		state, err := m.read(write)
		if err != nil {
			return nil, err
		}
		if state.Override || state.Weight != m.Baseline {
			return nil, fmt.Errorf("unclean baseline for %s: weight=%d override=%t", write.PodUID, state.Weight, state.Override)
		}
	}

	applied := make([]iocostintent.PlannedWrite, 0, len(plan.Treatment))
	for _, write := range plan.Treatment {
		if err := m.IO.Write(m.file(write), write.Value); err != nil {
			if rollbackErr := m.rollbackApplied(applied); rollbackErr != nil {
				return nil, fmt.Errorf("write treatment for %s: %w; rollback also failed: %v", write.PodUID, err, rollbackErr)
			}
			return nil, fmt.Errorf("write treatment for %s: %w", write.PodUID, err)
		}
		applied = append(applied, write)
	}

	result := make([]EffectiveWeight, 0, len(plan.Treatment))
	for _, write := range plan.Treatment {
		state, err := m.read(write)
		if err != nil {
			if rollbackErr := m.rollbackApplied(applied); rollbackErr != nil {
				return nil, fmt.Errorf("%w; rollback also failed: %v", err, rollbackErr)
			}
			return nil, err
		}
		expected, _, err := iocostintent.ParseDeviceWeight(fmt.Sprintf("default %d\n%s\n", m.Baseline, write.Value), m.Device)
		if err != nil || !state.Override || state.Weight != expected {
			if rollbackErr := m.rollbackApplied(applied); rollbackErr != nil {
				return nil, fmt.Errorf("readback mismatch for %s: got weight=%d override=%t expected=%d; rollback also failed: %v", write.PodUID, state.Weight, state.Override, expected, rollbackErr)
			}
			return nil, fmt.Errorf("readback mismatch for %s: got weight=%d override=%t expected=%d", write.PodUID, state.Weight, state.Override, expected)
		}
		result = append(result, state)
	}
	return result, nil
}

func (m Materializer) Read(plan iocostintent.Plan) ([]EffectiveWeight, error) {
	if m.IO == nil {
		m.IO = OSFileIO{}
	}
	result := make([]EffectiveWeight, 0, len(plan.Treatment))
	for _, write := range plan.Treatment {
		state, err := m.read(write)
		if err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, nil
}

func (m Materializer) Rollback(plan iocostintent.Plan) ([]EffectiveWeight, error) {
	if m.IO == nil {
		m.IO = OSFileIO{}
	}
	for _, write := range plan.Treatment {
		if err := m.IO.Write(m.file(write), m.Device+" default"); err != nil {
			return nil, fmt.Errorf("remove device override for %s: %w", write.PodUID, err)
		}
	}
	result := make([]EffectiveWeight, 0, len(plan.Treatment))
	for _, write := range plan.Treatment {
		state, err := m.read(write)
		if err != nil {
			return nil, err
		}
		if state.Override || state.Weight != m.Baseline {
			return nil, fmt.Errorf("rollback mismatch for %s: weight=%d override=%t", write.PodUID, state.Weight, state.Override)
		}
		result = append(result, state)
	}
	return result, nil
}

func (m Materializer) read(write iocostintent.PlannedWrite) (EffectiveWeight, error) {
	content, err := m.IO.Read(m.file(write))
	if err != nil {
		return EffectiveWeight{}, fmt.Errorf("read io.weight for %s: %w", write.PodUID, err)
	}
	weight, override, err := iocostintent.ParseDeviceWeight(content, m.Device)
	if err != nil {
		return EffectiveWeight{}, fmt.Errorf("parse io.weight for %s: %w", write.PodUID, err)
	}
	return EffectiveWeight{Application: write.Application, PodUID: write.PodUID, CgroupPath: write.CgroupPath, Weight: weight, Override: override}, nil
}

func (m Materializer) file(write iocostintent.PlannedWrite) string {
	return filepath.Join(m.Root, write.CgroupPath, write.File)
}

func (m Materializer) rollbackApplied(applied []iocostintent.PlannedWrite) error {
	var failures []string
	for _, write := range applied {
		if err := m.IO.Write(m.file(write), m.Device+" default"); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", write.PodUID, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

// DiscoverPodCgroup returns exactly one Pod-level cgroup beneath
// kubepods.slice. Symlinks and paths without io.weight are ignored.
func DiscoverPodCgroup(root, podUID string) (string, error) {
	uidToken := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(podUID)), "-", "_")
	if uidToken == "" {
		return "", fmt.Errorf("empty Pod UID")
	}
	base := filepath.Join(root, "kubepods.slice")
	var matches []string
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() || !strings.Contains(strings.ToLower(entry.Name()), uidToken) {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "io.weight")); err == nil {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			matches = append(matches, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("Pod UID %s resolved to %d cgroups", podUID, len(matches))
	}
	return matches[0], nil
}
