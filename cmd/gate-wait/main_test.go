package main

import (
	"os"
	"testing"
)

func clearGateEnv(t *testing.T) {
	t.Helper()
	for _, suffix := range []string{"A", "B"} {
		for _, key := range []string{"GATE_POD_UID_", "GATE_EXPECTED_WEIGHT_", "GATE_START_FILE_"} {
			t.Setenv(key+suffix, "")
			os.Unsetenv(key + suffix)
		}
	}
}

func TestLoadTargets_MissingEnvFailsClosed(t *testing.T) {
	clearGateEnv(t)
	if _, err := loadTargets(); err == nil {
		t.Fatal("expected an error when no target env vars are set")
	}
}

func TestLoadTargets_PartialEnvFailsClosed(t *testing.T) {
	clearGateEnv(t)
	t.Setenv("GATE_POD_UID_A", "aaaa")
	t.Setenv("GATE_EXPECTED_WEIGHT_A", "100")
	t.Setenv("GATE_START_FILE_A", "/gate-host/app-a/start")
	// B deliberately left unset.
	if _, err := loadTargets(); err == nil {
		t.Fatal("expected an error when only one of two targets is configured")
	}
}

func TestLoadTargets_InvalidWeightFailsClosed(t *testing.T) {
	clearGateEnv(t)
	t.Setenv("GATE_POD_UID_A", "aaaa")
	t.Setenv("GATE_EXPECTED_WEIGHT_A", "not-a-number")
	t.Setenv("GATE_START_FILE_A", "/gate-host/app-a/start")
	t.Setenv("GATE_POD_UID_B", "bbbb")
	t.Setenv("GATE_EXPECTED_WEIGHT_B", "100")
	t.Setenv("GATE_START_FILE_B", "/gate-host/app-b/start")
	if _, err := loadTargets(); err == nil {
		t.Fatal("expected an error for a non-numeric expected weight")
	}
}

func TestLoadTargets_Valid(t *testing.T) {
	clearGateEnv(t)
	t.Setenv("GATE_POD_UID_A", "aaaa")
	t.Setenv("GATE_EXPECTED_WEIGHT_A", "300")
	t.Setenv("GATE_START_FILE_A", "/gate-host/app-a/start")
	t.Setenv("GATE_POD_UID_B", "bbbb")
	t.Setenv("GATE_EXPECTED_WEIGHT_B", "100")
	t.Setenv("GATE_START_FILE_B", "/gate-host/app-b/start")
	targets, err := loadTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected exactly 2 targets, got %d", len(targets))
	}
	if targets[0].podUID != "aaaa" || targets[0].weight != 300 {
		t.Fatalf("unexpected target A: %+v", targets[0])
	}
	if targets[1].podUID != "bbbb" || targets[1].weight != 100 {
		t.Fatalf("unexpected target B: %+v", targets[1])
	}
}
