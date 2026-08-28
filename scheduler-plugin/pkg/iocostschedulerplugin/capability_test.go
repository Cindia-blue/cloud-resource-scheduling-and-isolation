package iocostschedulerplugin

import (
	"testing"
	"time"
)

func validRecord() Record {
	return Record{
		NodeName:      "node-a",
		NodeUID:       "uid-a",
		ProviderID:    "aws:///us-east-1e/node-a",
		DeviceMajMin:  "259:0",
		DeviceClass:   "gp3,16000,1000",
		ModelIdentity: "model-sha-abc123",
		Generation:    1,
		QualifiedAt:   time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}
}

func TestEvaluateNode_Qualified(t *testing.T) {
	inv := Inventory{Records: []Record{validRecord()}}
	now := validRecord().QualifiedAt.Add(time.Minute)
	ok, reason := evaluateNode(inv, "node-a", "uid-a", "aws:///us-east-1e/node-a", now, 20*time.Minute)
	if !ok {
		t.Fatalf("expected qualified match, got reject: %s", reason)
	}
}

func TestEvaluateNode_MissingRecord(t *testing.T) {
	inv := Inventory{Records: []Record{validRecord()}}
	ok, reason := evaluateNode(inv, "node-b", "uid-b", "aws:///us-east-1e/node-b", time.Now(), 0)
	if ok {
		t.Fatal("expected reject for node with no record")
	}
	if reason != "no capability record for this node" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestEvaluateNode_StaleNodeUID(t *testing.T) {
	inv := Inventory{Records: []Record{validRecord()}}
	ok, reason := evaluateNode(inv, "node-a", "uid-REPLACED", "aws:///us-east-1e/node-a", time.Now(), 0)
	if ok {
		t.Fatal("expected reject for stale Node UID")
	}
	if got := reason; got == "" || got[:5] != "stale" {
		t.Fatalf("expected a stale-UID reason, got: %s", got)
	}
}

func TestEvaluateNode_ProviderIDMismatch(t *testing.T) {
	inv := Inventory{Records: []Record{validRecord()}}
	ok, reason := evaluateNode(inv, "node-a", "uid-a", "aws:///us-east-1c/some-other-node", time.Now(), 0)
	if ok {
		t.Fatal("expected reject for providerID mismatch")
	}
	if got := reason; len(got) < 10 || got[:10] != "providerID" {
		t.Fatalf("expected a providerID-mismatch reason, got: %s", got)
	}
}

func TestEvaluateNode_UnresolvedDeviceMajMin(t *testing.T) {
	r := validRecord()
	r.DeviceMajMin = ""
	inv := Inventory{Records: []Record{r}}
	ok, reason := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for unresolved device major:minor")
	}
	if got := reason; got == "" {
		t.Fatal("expected a non-empty rejection reason")
	}
}

func TestEvaluateNode_UnresolvedDeviceClass(t *testing.T) {
	r := validRecord()
	r.DeviceClass = ""
	inv := Inventory{Records: []Record{r}}
	ok, _ := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for unresolved device class")
	}
}

func TestEvaluateNode_UnqualifiedModel(t *testing.T) {
	r := validRecord()
	r.ModelIdentity = ""
	inv := Inventory{Records: []Record{r}}
	ok, reason := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for missing model identity")
	}
	if got := reason; len(got) < 11 || got[:11] != "unqualified" {
		t.Fatalf("expected an unqualified-model reason, got: %s", got)
	}
}

func TestEvaluateNode_AmbiguousDuplicateRecords(t *testing.T) {
	r1 := validRecord()
	r2 := validRecord()
	r2.NodeUID = "uid-a-different-claim" // still claims the same NodeName
	inv := Inventory{Records: []Record{r1, r2}}
	ok, reason := evaluateNode(inv, "node-a", "uid-a", r1.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for ambiguous/duplicate records")
	}
	if got := reason; len(got) < 10 || got[:10] != "ambiguous:" {
		t.Fatalf("expected an ambiguous-records reason, got: %s", got)
	}
}

func TestEvaluateNode_MalformedRecord_MissingGeneration(t *testing.T) {
	r := validRecord()
	r.Generation = 0
	inv := Inventory{Records: []Record{r}}
	ok, _ := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for missing/non-positive generation")
	}
}

func TestEvaluateNode_MalformedRecord_MissingQualifiedAt(t *testing.T) {
	r := validRecord()
	r.QualifiedAt = time.Time{}
	inv := Inventory{Records: []Record{r}}
	ok, _ := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, time.Now(), 0)
	if ok {
		t.Fatal("expected reject for missing qualification timestamp")
	}
}

func TestEvaluateNode_StaleGeneration_MaxAgeExceeded(t *testing.T) {
	r := validRecord()
	inv := Inventory{Records: []Record{r}}
	tooLate := r.QualifiedAt.Add(21 * time.Minute)
	ok, reason := evaluateNode(inv, r.NodeName, r.NodeUID, r.ProviderID, tooLate, 20*time.Minute)
	if ok {
		t.Fatal("expected reject for a record older than maxAge")
	}
	if got := reason; len(got) < 5 || got[:5] != "stale" {
		t.Fatalf("expected a stale-record reason, got: %s", got)
	}
}

func TestEvaluateNode_NodeReplacedUnderSameName(t *testing.T) {
	// Simulates node-replacement: same Node name, but Karpenter has since
	// replaced the underlying instance, so the live UID and providerID no
	// longer match the record collected for the original instance.
	r := validRecord()
	inv := Inventory{Records: []Record{r}}
	ok, reason := evaluateNode(inv, r.NodeName, "uid-of-REPLACEMENT-node", "aws:///us-east-1e/i-brandnew", time.Now(), 0)
	if ok {
		t.Fatal("expected reject when the node has been replaced under the same name")
	}
	if reason == "" {
		t.Fatal("expected a non-empty rejection reason")
	}
}

func TestParseInventory_Empty(t *testing.T) {
	if _, err := ParseInventory(""); err == nil {
		t.Fatal("expected error for empty inventory data")
	}
}

func TestParseInventory_Malformed(t *testing.T) {
	if _, err := ParseInventory("{not json"); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseInventory_Valid(t *testing.T) {
	inv, err := ParseInventory(`{"records":[{"nodeName":"node-a","nodeUID":"uid-a"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inv.Records) != 1 || inv.Records[0].NodeName != "node-a" {
		t.Fatalf("unexpected parse result: %+v", inv)
	}
}
