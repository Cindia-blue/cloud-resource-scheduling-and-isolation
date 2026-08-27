package targetrecord

import (
	"strings"
	"testing"
	"time"
)

func validRecord() Record {
	return Record{
		ClusterID:          "compute-us-east-1-test-cindy1",
		Context:            "compute-us-east-1-test-cindy1-oidc",
		NodeName:           "i-0aec3ddfbd9921080",
		NodeUID:            "9141e82a-8bfb-4902-906c-3ec7b5da6e6d",
		ProviderID:         "aws:///us-east-1c/i-0aec3ddfbd9921080",
		InstanceID:         "i-0aec3ddfbd9921080",
		DataVolumeID:       "vol-0e0ee6777535000a9",
		NVMeDevice:         "nvme1n1",
		DeviceMajMin:       "259:0",
		EBSType:            "gp3",
		EBSIOPS:            16000,
		EBSThroughputMiBps: 1000,
		CollectedAt:        "2026-08-26T21:00:00Z",
	}
}

func TestValidate_AcceptsCompleteRecord(t *testing.T) {
	if err := Validate(validRecord()); err != nil {
		t.Fatal(err)
	}
}

func TestValidate_RejectsUnresolvedPlaceholder(t *testing.T) {
	r := validRecord()
	r.NodeName = "__NODE_NAME__"
	if err := Validate(r); err == nil {
		t.Fatal("expected rejection of unresolved placeholder")
	}
}

func TestValidate_RejectsEmptyField(t *testing.T) {
	r := validRecord()
	r.DataVolumeID = ""
	if err := Validate(r); err == nil {
		t.Fatal("expected rejection of empty field")
	}
}

func TestMaxAge_RejectsStaleRecord(t *testing.T) {
	r := validRecord()
	now, err := time.Parse(time.RFC3339, "2026-08-26T22:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if err := MaxAge(r, now, 30*time.Minute); err == nil {
		t.Fatal("expected rejection of a record older than the max age")
	}
}

// Replaced Node with reused name: Karpenter (or any controller) deletes and
// recreates a Node object under the same metadata.name. The name matches but
// the UID never will; this must be rejected, not silently adopted.
func TestReconfirmIdentity_RejectsReplacedNodeReusedName(t *testing.T) {
	r := validRecord()
	live := LiveNodeIdentity{Found: true, NodeUID: "11111111-1111-1111-1111-111111111111", ProviderID: r.ProviderID}
	if err := ReconfirmIdentity(r, live); err == nil {
		t.Fatal("expected rejection when live UID differs from the frozen record under the same node name")
	}
}

func TestReconfirmIdentity_RejectsStaleUID(t *testing.T) {
	r := validRecord()
	r.NodeUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" // frozen at render time
	live := LiveNodeIdentity{Found: true, NodeUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", ProviderID: r.ProviderID}
	if err := ReconfirmIdentity(r, live); err == nil {
		t.Fatal("expected rejection of a stale frozen UID that no longer matches live state")
	}
}

func TestReconfirmIdentity_RejectsChangedProviderID(t *testing.T) {
	r := validRecord()
	live := LiveNodeIdentity{Found: true, NodeUID: r.NodeUID, ProviderID: "aws:///us-east-1c/i-0000000000000000f"}
	if err := ReconfirmIdentity(r, live); err == nil {
		t.Fatal("expected rejection when providerID changed")
	}
}

func TestReconfirmIdentity_RejectsDeletedNode(t *testing.T) {
	r := validRecord()
	live := LiveNodeIdentity{Found: false}
	err := ReconfirmIdentity(r, live)
	if err == nil {
		t.Fatal("expected rejection of a deleted node")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
}

func TestReconfirmDevice_RejectsVolumeMismatch(t *testing.T) {
	r := validRecord()
	live := LiveDeviceIdentity{
		DataVolumeID: "vol-0000000000000000f", DeviceMajMin: r.DeviceMajMin,
		EBSType: r.EBSType, EBSIOPS: r.EBSIOPS, EBSThroughputMiBps: r.EBSThroughputMiBps,
	}
	if err := ReconfirmDevice(r, live); err == nil {
		t.Fatal("expected rejection of a data volume mismatch")
	}
}

func TestReconfirmDevice_RejectsMajMinMismatch(t *testing.T) {
	r := validRecord()
	live := LiveDeviceIdentity{
		DataVolumeID: r.DataVolumeID, DeviceMajMin: "259:1",
		EBSType: r.EBSType, EBSIOPS: r.EBSIOPS, EBSThroughputMiBps: r.EBSThroughputMiBps,
	}
	if err := ReconfirmDevice(r, live); err == nil {
		t.Fatal("expected rejection when major:minor no longer matches (major:minor alone is never sufficient identity)")
	}
}

func TestReconfirmDevice_RejectsPerformanceClassMismatch(t *testing.T) {
	r := validRecord()
	live := LiveDeviceIdentity{
		DataVolumeID: r.DataVolumeID, DeviceMajMin: r.DeviceMajMin,
		EBSType: r.EBSType, EBSIOPS: 3000, EBSThroughputMiBps: 125,
	}
	if err := ReconfirmDevice(r, live); err == nil {
		t.Fatal("expected rejection of an EBS performance-class mismatch")
	}
}

func testCluster() map[string]NodeInternalIdentity {
	return map[string]NodeInternalIdentity{
		"i-083d718a1260c191f": {NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"},
		"i-0c20b0a3e32684a4c": {NodeName: "i-0c20b0a3e32684a4c", InternalIP: "10.243.180.152", InternalDNS: "ip-10-243-180-152.ec2.internal"},
	}
}

// This is the corrected behavior: a CN using the node's own EC2-default
// internal DNS name (not its Kubernetes Node name/instance ID) is valid --
// requiring CN == instance ID specifically was the earlier over-strict
// interpretation.
func TestReconfirmCertificateCN_AcceptsSelfReferentialInternalDNSForm(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	if err := ReconfirmCertificateCN(target, "ip-10-243-175-196.ec2.internal", testCluster()); err != nil {
		t.Fatal(err)
	}
}

func TestReconfirmCertificateCN_AcceptsNodeNameForm(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	if err := ReconfirmCertificateCN(target, "i-083d718a1260c191f", testCluster()); err != nil {
		t.Fatal(err)
	}
}

func TestReconfirmCertificateCN_RejectsCrossNodeConfusion(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	// This CN actually belongs to the OTHER node in the cluster map.
	err := ReconfirmCertificateCN(target, "ip-10-243-180-152.ec2.internal", testCluster())
	if err == nil {
		t.Fatal("expected rejection of a CN that resolves to a different node")
	}
	if !strings.Contains(err.Error(), "cross-node") {
		t.Fatalf("expected a cross-node-confusion error, got: %v", err)
	}
}

func TestReconfirmCertificateCN_RejectsUnresolvedCN(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	err := ReconfirmCertificateCN(target, "ip-10-99-99-99.ec2.internal", testCluster())
	if err == nil {
		t.Fatal("expected rejection of a CN that matches no known node")
	}
	if !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("expected an unresolved error, got: %v", err)
	}
}

func TestReconfirmCertificateCN_RejectsDuplicateMapping(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	cluster := map[string]NodeInternalIdentity{
		"i-083d718a1260c191f": target,
		"i-duplicate":         {NodeName: "i-duplicate", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"},
	}
	err := ReconfirmCertificateCN(target, "ip-10-243-175-196.ec2.internal", cluster)
	if err == nil {
		t.Fatal("expected rejection of a CN that resolves to more than one node")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected a duplicate/ambiguous error, got: %v", err)
	}
}

func TestReconfirmCertificateCN_RejectsEmptyCN(t *testing.T) {
	target := NodeInternalIdentity{NodeName: "i-083d718a1260c191f", InternalIP: "10.243.175.196", InternalDNS: "ip-10-243-175-196.ec2.internal"}
	if err := ReconfirmCertificateCN(target, "", testCluster()); err == nil {
		t.Fatal("expected rejection of an empty observed CN")
	}
}

func TestRenderPlaceholders_SubstitutesAllFields(t *testing.T) {
	tmpl := "node=__NODE_NAME__ device=__DEVICE_MAJMIN__ volume=__DATA_VOLUME_ID__"
	out, err := RenderPlaceholders(tmpl, validRecord())
	if err != nil {
		t.Fatal(err)
	}
	want := "node=i-0aec3ddfbd9921080 device=259:0 volume=vol-0e0ee6777535000a9"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderPlaceholders_RejectsUnresolvedPlaceholder(t *testing.T) {
	tmpl := "node=__NODE_NAME__ unknown=__NOT_A_REAL_FIELD__"
	if _, err := RenderPlaceholders(tmpl, validRecord()); err == nil {
		t.Fatal("expected rejection of an unmapped placeholder left in the render")
	}
}
