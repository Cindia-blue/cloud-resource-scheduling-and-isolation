// Package iocostschedulerplugin implements a minimal, fail-closed Filter
// plugin that routes an explicitly opted-in, IOCost-protection-intent Pod
// only onto a Node that exactly matches one current, unambiguous
// capability record. It proves capability-aware routing across multiple
// candidate nodes; it is not a fleet-optimization, load-balancing, or
// noisy-neighbor-prediction mechanism, and production capability
// publication/model distribution remain unsolved (see
// docs/architecture.md and docs/limitations.md in the parent module).
package iocostschedulerplugin

import (
	"encoding/json"
	"fmt"
	"time"
)

// Record binds one Node's IOCost qualification to the exact identity
// facts this plugin requires before it will ever route a protected Pod
// onto it. It is generated from the same targetrecord.Record used to
// qualify the node for the adapter (see cmd/render-target in the parent
// module) -- never hand-copied from prose -- so the capability record
// and the adapter's own target record share one source of truth.
type Record struct {
	NodeName     string `json:"nodeName"`
	NodeUID      string `json:"nodeUID"`
	ProviderID   string `json:"providerID"`
	DeviceMajMin string `json:"deviceMajMin"`
	DeviceClass  string `json:"deviceClass"` // e.g. "gp3,16000,1000"
	// ModelIdentity is an opaque identifier for the reviewed io.cost.model
	// this Node was qualified with -- e.g. a content hash of the model/QoS
	// fields, or a reviewed config's own version tag. The plugin never
	// inspects or guesses model coefficients; it only requires this field
	// to be present and non-empty, meaning "a human-reviewed model was
	// bound to this record."
	ModelIdentity string    `json:"modelIdentity"`
	Generation    int64     `json:"generation"`
	QualifiedAt   time.Time `json:"qualifiedAt"`
}

// Inventory is the ConfigMap-backed capability record set. It is
// intentionally a flat list, not a map keyed by node name, so that a
// duplicate/ambiguous claim on one node is a detectable data condition
// (more than one entry with the same NodeName) rather than something a
// map's last-write-wins semantics would silently hide.
type Inventory struct {
	Records []Record `json:"records"`
}

// ParseInventory parses the ConfigMap's single data key. It does not
// validate freshness or completeness of individual records -- that is
// evaluateNode's job, once a specific Node is being checked.
func ParseInventory(data string) (Inventory, error) {
	var inv Inventory
	if data == "" {
		return inv, fmt.Errorf("empty capability inventory")
	}
	if err := json.Unmarshal([]byte(data), &inv); err != nil {
		return inv, fmt.Errorf("parse capability inventory: %w", err)
	}
	return inv, nil
}

// recordsForNode returns every record in the inventory claiming this
// exact Node name. More than one is an ambiguous/duplicate condition the
// caller must reject, not resolve by picking one.
func recordsForNode(inv Inventory, nodeName string) []Record {
	var matches []Record
	for _, r := range inv.Records {
		if r.NodeName == nodeName {
			matches = append(matches, r)
		}
	}
	return matches
}

// evaluateNode is the pure decision function behind the Filter plugin,
// kept separate from any Kubernetes/framework types so it is trivially
// unit-testable. liveNodeUID and liveProviderID are the live values read
// from the Node object under evaluation right now -- never trusted from
// the record alone, since the whole point of binding on UID/providerID
// is to catch a node that has been replaced under the same name.
func evaluateNode(inv Inventory, nodeName, liveNodeUID, liveProviderID string, now time.Time, maxAge time.Duration) (ok bool, reason string) {
	matches := recordsForNode(inv, nodeName)
	if len(matches) == 0 {
		return false, "no capability record for this node"
	}
	if len(matches) > 1 {
		return false, "ambiguous: multiple capability records claim this node"
	}
	r := matches[0]

	if r.NodeUID == "" || liveNodeUID == "" {
		return false, "malformed record or node: missing Node UID"
	}
	if r.NodeUID != liveNodeUID {
		return false, fmt.Sprintf("stale Node UID: record has %q, live node is %q", r.NodeUID, liveNodeUID)
	}
	if r.ProviderID == "" || liveProviderID == "" {
		return false, "malformed record or node: missing providerID"
	}
	if r.ProviderID != liveProviderID {
		return false, fmt.Sprintf("providerID mismatch: record has %q, live node is %q", r.ProviderID, liveProviderID)
	}
	if r.DeviceMajMin == "" {
		return false, "unresolved device: record has no device major:minor"
	}
	if r.DeviceClass == "" {
		return false, "unresolved device: record has no device class"
	}
	if r.ModelIdentity == "" {
		return false, "unqualified: record has no reviewed IOCost model identity"
	}
	if r.Generation <= 0 {
		return false, "malformed record: missing or non-positive generation"
	}
	if r.QualifiedAt.IsZero() {
		return false, "malformed record: missing qualification timestamp"
	}
	if maxAge > 0 && now.Sub(r.QualifiedAt) > maxAge {
		return false, fmt.Sprintf("stale record: qualified %s ago, max age is %s", now.Sub(r.QualifiedAt), maxAge)
	}

	return true, "exact match: node UID, providerID, device, and model identity all confirmed"
}
