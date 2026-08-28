// render-capability-record derives one scheduler capability-inventory
// record from the same targetrecord.Record already used to qualify a
// node for the iocost-adapter (see cmd/render-target), so the adapter's
// identity binding and the scheduler's routing eligibility are always
// generated from one source of truth -- never hand-copied from prose.
//
// It performs no cluster or node I/O itself; the input record's fields
// must already be freshly collected (see cmd/render-target's own
// doc comment).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"sigs.k8s.io/IOIsolation/pkg/targetrecord"
)

// capabilityRecord mirrors iocostschedulerplugin.Record's JSON schema
// (scheduler-plugin/pkg/iocostschedulerplugin/capability.go) by field
// name convention -- the two packages live in separate Go modules
// (see docs/architecture.md for why) and are deliberately not
// code-coupled, only JSON-schema-coupled.
type capabilityRecord struct {
	NodeName      string    `json:"nodeName"`
	NodeUID       string    `json:"nodeUID"`
	ProviderID    string    `json:"providerID"`
	DeviceMajMin  string    `json:"deviceMajMin"`
	DeviceClass   string    `json:"deviceClass"`
	ModelIdentity string    `json:"modelIdentity"`
	Generation    int64     `json:"generation"`
	QualifiedAt   time.Time `json:"qualifiedAt"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "render-capability-record: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		recordJSON    string
		modelIdentity string
		outPath       string
		maxAge        time.Duration
	)
	flag.StringVar(&recordJSON, "record", "", "path to a target record JSON file (see cmd/render-target)")
	flag.StringVar(&modelIdentity, "model-identity", "", "opaque identifier for the reviewed IOCost model this node was qualified with (required, non-empty)")
	flag.StringVar(&outPath, "out", "", "path to write the capability record JSON")
	flag.DurationVar(&maxAge, "max-age", 20*time.Minute, "reject a target record collected longer ago than this")
	flag.Parse()

	if recordJSON == "" || modelIdentity == "" || outPath == "" {
		return fmt.Errorf("-record, -model-identity, and -out are required")
	}

	raw, err := os.ReadFile(recordJSON)
	if err != nil {
		return fmt.Errorf("read target record: %w", err)
	}
	var r targetrecord.Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("parse target record: %w", err)
	}
	if err := targetrecord.Validate(r); err != nil {
		return fmt.Errorf("target record rejected: %w", err)
	}
	if err := targetrecord.MaxAge(r, time.Now().UTC(), maxAge); err != nil {
		return fmt.Errorf("target record rejected: %w", err)
	}
	if r.DeviceMajMin == "" {
		return fmt.Errorf("target record has no resolved DeviceMajMin")
	}
	if r.EBSType == "" || r.EBSIOPS <= 0 || r.EBSThroughputMiBps <= 0 {
		return fmt.Errorf("target record has an incomplete EBS class (type/IOPS/throughput)")
	}
	qualifiedAt, err := time.Parse(time.RFC3339, r.CollectedAt)
	if err != nil {
		return fmt.Errorf("target record CollectedAt is not RFC3339: %w", err)
	}

	rec := capabilityRecord{
		NodeName:      r.NodeName,
		NodeUID:       r.NodeUID,
		ProviderID:    r.ProviderID,
		DeviceMajMin:  r.DeviceMajMin,
		DeviceClass:   fmt.Sprintf("%s,%d,%d", r.EBSType, r.EBSIOPS, r.EBSThroughputMiBps),
		ModelIdentity: modelIdentity,
		// Generation is the target record's own collection time, in unix
		// seconds -- monotonic enough to detect a stale/superseded record
		// in this bounded, single-experiment scope. It is not a general
		// revision-counter mechanism (see docs/limitations.md).
		Generation:  qualifiedAt.Unix(),
		QualifiedAt: qualifiedAt,
	}

	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal capability record: %w", err)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Println(outPath)
	return nil
}
