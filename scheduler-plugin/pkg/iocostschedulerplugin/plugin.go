package iocostschedulerplugin

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Name is the plugin name used in the KubeSchedulerConfiguration profile.
const Name = "IOCostCapability"

// ProtectionIntentAnnotation is the explicit, existing experimental
// protection-intent annotation this plugin requires. It intentionally
// reuses the same key the iocost-adapter already watches
// (cindy1.poc/io-weight), so a Pod's declared intent and its scheduling
// eligibility are expressed through the same one field, not two that
// could disagree.
const ProtectionIntentAnnotation = "cindy1.poc/io-weight"

// InventoryConfigMapEnv/Namespace are read once at plugin construction.
// The inventory ConfigMap is a dedicated, removable, experiment-scoped
// object -- never a Node label/taint (see docs/architecture.md).
type Args struct {
	Namespace     string
	ConfigMapName string
	MaxAge        time.Duration
}

type IOCostCapability struct {
	client    kubernetes.Interface
	namespace string
	cmName    string
	maxAge    time.Duration
	now       func() time.Time
}

var _ framework.FilterPlugin = &IOCostCapability{}

func (p *IOCostCapability) Name() string { return Name }

// NewFactory returns a runtime.PluginFactory closure over args, wired
// into the scheduler binary's plugin registry (see
// cmd/ioi-capability-scheduler). Args come from the scheduler binary's
// own flags/env, not from KubeSchedulerConfiguration's pluginConfig --
// this is a bounded experiment with exactly one inventory ConfigMap, not
// a general capability-publication system, so a full custom-args API
// type would be unjustified ceremony.
func NewFactory(args Args) func(ctx context.Context, obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	return func(ctx context.Context, obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
		return &IOCostCapability{
			client:    handle.ClientSet(),
			namespace: args.Namespace,
			cmName:    args.ConfigMapName,
			maxAge:    args.MaxAge,
			now:       time.Now,
		}, nil
	}
}

// Filter requires the explicit protection-intent annotation, loads the
// dedicated capability ConfigMap fresh (no local cache -- this is a
// bounded, low-QPS experiment, not a production-scale plugin), and
// fails closed on every ambiguous or incomplete condition. A malformed
// Pod intent, a missing/unreadable ConfigMap, or any mismatch all result
// in framework.Unschedulable with a concise, inspectable reason -- never
// framework.Error, and never a silent pass-through to another scheduler.
func (p *IOCostCapability) Filter(ctx context.Context, _ fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) *fwk.Status {
	intent, ok := pod.Annotations[ProtectionIntentAnnotation]
	if !ok || intent == "" {
		return fwk.NewStatus(fwk.Unschedulable,
			"malformed intent: Pod is missing required "+ProtectionIntentAnnotation+" annotation")
	}

	node := nodeInfo.Node()
	if node == nil {
		return fwk.NewStatus(fwk.Unschedulable, "no live Node object for this candidate")
	}

	cm, err := p.client.CoreV1().ConfigMaps(p.namespace).Get(ctx, p.cmName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fwk.NewStatus(fwk.Unschedulable,
				"missing qualification: no capability inventory ConfigMap found")
		}
		return fwk.NewStatus(fwk.Unschedulable,
			"missing qualification: capability inventory ConfigMap unreadable: "+err.Error())
	}

	inv, err := ParseInventory(cm.Data["records.json"])
	if err != nil {
		return fwk.NewStatus(fwk.Unschedulable,
			"malformed record: "+err.Error())
	}

	ok, reason := evaluateNode(inv, node.Name, string(node.UID), node.Spec.ProviderID, p.now(), p.maxAge)
	if !ok {
		return fwk.NewStatus(fwk.Unschedulable, reason)
	}
	return nil
}
