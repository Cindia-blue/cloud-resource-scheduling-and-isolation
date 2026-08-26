package iocostadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/IOIsolation/pkg/iocostintent"
)

type fakeCgroupIO struct{ values map[string]string }

func (f *fakeCgroupIO) Read(path string) (string, error) {
	v, ok := f.values[path]
	if !ok {
		return "", fmt.Errorf("missing %s", path)
	}
	return v, nil
}

func (f *fakeCgroupIO) Write(path, value string) error {
	fields := strings.Fields(value)
	if len(fields) != 2 {
		return fmt.Errorf("bad write %q", value)
	}
	current := f.values[path]
	lines := strings.Split(strings.TrimSpace(current), "\n")
	out := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, fields[0]+" ") {
			out = append(out, line)
		}
	}
	if fields[1] != "default" {
		out = append(out, value)
	}
	f.values[path] = strings.Join(out, "\n") + "\n"
	return nil
}

func testPlan(t *testing.T, aWeight, bWeight uint32) iocostintent.Plan {
	t.Helper()
	intent := iocostintent.Intent{
		Device:          "259:1",
		Protected:       iocostintent.ApplicationBinding{Application: "app-a", PodUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", CgroupPath: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podaaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee.slice"},
		Competing:       iocostintent.ApplicationBinding{Application: "app-b", PodUID: "11111111-2222-3333-4444-555555555555", CgroupPath: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod11111111_2222_3333_4444_555555555555.slice"},
		ProtectedWeight: aWeight, CompetingWeight: bWeight, BaselineWeight: 100,
	}
	plan, err := iocostintent.BuildPlan(intent)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSymmetricTreatmentCreatesExplicitOverridesAndRollbackRemovesThem(t *testing.T) {
	plan := testPlan(t, 100, 100)
	values := map[string]string{}
	for _, write := range plan.Treatment {
		values[filepath.Join("/cg", write.CgroupPath, "io.weight")] = "default 100\n"
	}
	io := &fakeCgroupIO{values: values}
	m := Materializer{Root: "/cg", Device: "259:1", Baseline: 100, IO: io}
	states, err := m.Apply(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if !state.Override || state.Weight != 100 {
			t.Fatalf("treatment did not materialize: %+v", state)
		}
	}
	states, err = m.Rollback(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Override || state.Weight != 100 {
			t.Fatalf("rollback did not restore inherited baseline: %+v", state)
		}
	}
}

func TestApplyRejectsStaleOverride(t *testing.T) {
	plan := testPlan(t, 300, 100)
	values := map[string]string{}
	for i, write := range plan.Treatment {
		value := "default 100\n"
		if i == 0 {
			value += "259:1 200\n"
		}
		values[filepath.Join("/cg", write.CgroupPath, "io.weight")] = value
	}
	m := Materializer{Root: "/cg", Device: "259:1", Baseline: 100, IO: &fakeCgroupIO{values: values}}
	if _, err := m.Apply(plan); err == nil {
		t.Fatal("expected stale override rejection")
	}
}

func TestDiscoverPodCgroupRequiresExactlyOneMatch(t *testing.T) {
	root := t.TempDir()
	uid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	path := filepath.Join(root, "kubepods.slice", "kubepods-besteffort.slice", "kubepods-besteffort-podaaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee.slice")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "io.weight"), []byte("default 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverPodCgroup(root, uid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "podaaaaaaaa_bbbb") {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestCheckControllerReady(t *testing.T) {
	io := &fakeCgroupIO{values: map[string]string{
		"/cg/io.cost.model": "259:1 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n",
		"/cg/io.cost.qos":   "259:1 enable=1 rpct=95.00 rlat=2500 wpct=95.00 wlat=5000 min=80.00 max=100.00\n",
	}}
	if err := CheckControllerReady("/cg", "259:1", io); err != nil {
		t.Fatal(err)
	}
	io.values["/cg/io.cost.qos"] = "259:1 enable=0\n"
	if err := CheckControllerReady("/cg", "259:1", io); err == nil {
		t.Fatal("expected disabled QoS rejection")
	}
	io.values["/cg/io.cost.qos"] = "259:1 enable=1\n"
	io.values["/cg/io.cost.model"] = "259:1 ctrl=user model=linear rbps=0 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n"
	if err := CheckControllerReady("/cg", "259:1", io); err == nil {
		t.Fatal("expected zero model coefficient rejection")
	}
	io.values["/cg/io.cost.model"] = "8:0 ctrl=user model=linear rbps=1 rseqiops=1 rrandiops=1 wbps=1 wseqiops=1 wrandiops=1\n"
	if err := CheckControllerReady("/cg", "259:1", io); err == nil {
		t.Fatal("expected missing target-device model rejection")
	}
}
