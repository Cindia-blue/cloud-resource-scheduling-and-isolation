package iocostintent

import (
	"strings"
	"testing"
)

func validIntent() Intent {
	return Intent{
		Device: "259:1",
		Protected: ApplicationBinding{
			Application: "protected-app",
			PodUID:      "36e170cd-a62a-40a3-8a0a-63d531536782",
			CgroupPath:  "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod36e170cd_a62a_40a3_8a0a_63d531536782.slice",
		},
		Competing: ApplicationBinding{
			Application: "competing-app",
			PodUID:      "775730e2-f250-4780-a98d-c35199ad9483",
			CgroupPath:  "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod775730e2_f250_4780_a98d_c35199ad9483.slice",
		},
		ProtectedWeight: 300,
		CompetingWeight: 100,
		BaselineWeight:  100,
	}
}

func TestBuildPlanUsesOnlyIOWeight(t *testing.T) {
	plan, err := BuildPlan(validIntent())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Treatment) != 2 || len(plan.Rollback) != 2 {
		t.Fatalf("bad plan: %+v", plan)
	}
	for _, write := range append(plan.Treatment, plan.Rollback...) {
		if write.File != "io.weight" || strings.Contains(write.File+write.Value, "io.max") {
			t.Fatalf("retired primitive leaked into plan: %+v", write)
		}
	}
	if plan.Treatment[0].Value != "259:1 300" || plan.Treatment[1].Value != "259:1 100" {
		t.Fatalf("unexpected treatment: %+v", plan.Treatment)
	}
}

func TestAllowsEqualWeightsForSymmetricControlCell(t *testing.T) {
	intent := validIntent()
	intent.CompetingWeight = intent.ProtectedWeight
	plan, err := BuildPlan(intent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Treatment[0].Value != plan.Treatment[1].Value {
		t.Fatalf("symmetric plan is not symmetric: %+v", plan.Treatment)
	}
}

func TestRejectsStalePodGeneration(t *testing.T) {
	intent := validIntent()
	intent.Protected.PodUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if err := Validate(intent); err == nil {
		t.Fatal("expected generation rejection")
	}
}

func TestParseDeviceWeight(t *testing.T) {
	weight, override, err := ParseDeviceWeight("default 100\n259:1 300\n", "259:1")
	if err != nil || !override || weight != 300 {
		t.Fatalf("got %d %v %v", weight, override, err)
	}
	weight, override, err = ParseDeviceWeight("default 100\n", "259:1")
	if err != nil || override || weight != 100 {
		t.Fatalf("got %d %v %v", weight, override, err)
	}
}
