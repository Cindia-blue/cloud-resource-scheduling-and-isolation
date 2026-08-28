package iocostschedulerplugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	fwk "k8s.io/kube-scheduler/framework"
)

// fakeNodeInfo is the minimal fwk.NodeInfo this test needs: only Node()
// is called by Filter.
type fakeNodeInfo struct {
	fwk.NodeInfo
	node *v1.Node
}

func (f *fakeNodeInfo) Node() *v1.Node { return f.node }

func newPlugin(t *testing.T, objs ...interface{}) *IOCostCapability {
	t.Helper()
	client := fake.NewSimpleClientset()
	for _, o := range objs {
		switch obj := o.(type) {
		case *v1.ConfigMap:
			if _, err := client.CoreV1().ConfigMaps(obj.Namespace).Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
				t.Fatalf("seed configmap: %v", err)
			}
		}
	}
	return &IOCostCapability{
		client:    client,
		namespace: "experiment-ns",
		cmName:    "ioi-capability-inventory",
		maxAge:    20 * time.Minute,
		now:       func() time.Time { return time.Date(2026, 8, 28, 0, 5, 0, 0, time.UTC) },
	}
}

func testNode(name, uid, providerID string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid)},
		Spec:       v1.NodeSpec{ProviderID: providerID},
	}
}

func recordCM(records ...Record) *v1.ConfigMap {
	inv := Inventory{Records: records}
	data, err := json.Marshal(inv)
	if err != nil {
		panic(err)
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "ioi-capability-inventory", Namespace: "experiment-ns"},
		Data:       map[string]string{"records.json": string(data)},
	}
}

func TestFilter_MissingIntentAnnotation(t *testing.T) {
	p := newPlugin(t)
	pod := &v1.Pod{}
	status := p.Filter(context.Background(), nil, pod, &fakeNodeInfo{node: testNode("node-a", "uid-a", "aws:///x/node-a")})
	if status == nil || status.Code() != fwk.Unschedulable {
		t.Fatalf("expected Unschedulable for missing intent annotation, got %v", status)
	}
}

func TestFilter_MissingConfigMap_FailsClosed(t *testing.T) {
	p := newPlugin(t) // no ConfigMap seeded
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{ProtectionIntentAnnotation: "100"}}}
	status := p.Filter(context.Background(), nil, pod, &fakeNodeInfo{node: testNode("node-a", "uid-a", "aws:///x/node-a")})
	if status == nil || status.Code() != fwk.Unschedulable {
		t.Fatalf("expected Unschedulable when the capability ConfigMap is missing, got %v", status)
	}
}

func TestFilter_QualifiedNode_Success(t *testing.T) {
	rec := Record{
		NodeName: "node-a", NodeUID: "uid-a", ProviderID: "aws:///x/node-a",
		DeviceMajMin: "259:0", DeviceClass: "gp3,16000,1000",
		ModelIdentity: "model-sha-1", Generation: 1,
		QualifiedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}
	p := newPlugin(t, recordCM(rec))
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{ProtectionIntentAnnotation: "100"}}}
	status := p.Filter(context.Background(), nil, pod, &fakeNodeInfo{node: testNode("node-a", "uid-a", "aws:///x/node-a")})
	if status != nil {
		t.Fatalf("expected Success (nil status) for a qualified node, got %v: %s", status.Code(), status.Message())
	}
}

func TestFilter_UnqualifiedCandidateNode_Rejected(t *testing.T) {
	rec := Record{
		NodeName: "node-a", NodeUID: "uid-a", ProviderID: "aws:///x/node-a",
		DeviceMajMin: "259:0", DeviceClass: "gp3,16000,1000",
		ModelIdentity: "model-sha-1", Generation: 1,
		QualifiedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	}
	p := newPlugin(t, recordCM(rec))
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{ProtectionIntentAnnotation: "100"}}}
	// A different candidate node with no matching record.
	status := p.Filter(context.Background(), nil, pod, &fakeNodeInfo{node: testNode("node-b", "uid-b", "aws:///x/node-b")})
	if status == nil || status.Code() != fwk.Unschedulable {
		t.Fatalf("expected Unschedulable for the unqualified candidate node, got %v", status)
	}
}
