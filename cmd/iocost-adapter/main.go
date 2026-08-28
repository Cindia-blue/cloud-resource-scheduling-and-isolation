package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"sigs.k8s.io/IOIsolation/pkg/iocostadapter"
	"sigs.k8s.io/IOIsolation/pkg/iocostintent"
)

const (
	weightAnnotation = "cindy1.poc/io-weight"
	cellLabel        = "cindy1.poc/cell"
	roleLabel        = "cindy1.poc/role"
)

type config struct {
	Mode             string
	Node             string
	Namespace        string
	ActiveCell       string
	Device           string
	DataMount        string
	RootMount        string
	SysRoot          string
	CgroupRoot       string
	Baseline         uint32
	Poll             time.Duration
	ReconcileTimeout time.Duration
	HTTPAddr         string
}

type controller struct {
	cfg          config
	client       kubernetes.Interface
	materializer iocostadapter.Materializer
	active       *iocostintent.Plan
	ready        atomic.Bool
}

type evidence struct {
	Time    string                          `json:"time"`
	Verdict string                          `json:"verdict"`
	Mode    string                          `json:"mode"`
	Cell    string                          `json:"cell,omitempty"`
	Node    string                          `json:"node"`
	Device  string                          `json:"device"`
	State   []iocostadapter.EffectiveWeight `json:"state,omitempty"`
	Error   string                          `json:"error,omitempty"`
}

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	cfg, err := loadConfig()
	if err != nil {
		klog.Fatalf("configuration rejected: %v", err)
	}
	device, err := iocostadapter.ResolveDataDevice(iocostadapter.OSDeviceStat{}, cfg.SysRoot, cfg.DataMount, cfg.RootMount)
	if err != nil {
		klog.Fatalf("resolve data device on %s: %v", cfg.Node, err)
	}
	cfg.Device = device
	klog.Infof("resolved data device on %s: %s (from %s)", cfg.Node, cfg.Device, cfg.DataMount)
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("in-cluster Kubernetes config: %v", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		klog.Fatalf("Kubernetes client: %v", err)
	}

	c := &controller{
		cfg:    cfg,
		client: client,
		materializer: iocostadapter.Materializer{
			Root: cfg.CgroupRoot, Device: cfg.Device, Baseline: cfg.Baseline,
		},
	}
	go c.serveHealth()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := c.run(ctx); err != nil {
		klog.Errorf("adapter stopped: %v", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	baseline64, err := strconv.ParseUint(env("BASELINE_WEIGHT", "100"), 10, 32)
	if err != nil {
		return config{}, fmt.Errorf("BASELINE_WEIGHT: %w", err)
	}
	poll, err := time.ParseDuration(env("POLL_INTERVAL", "2s"))
	if err != nil || poll < time.Second {
		return config{}, fmt.Errorf("POLL_INTERVAL must be at least 1s")
	}
	reconcileTimeout, err := time.ParseDuration(env("RECONCILE_TIMEOUT", "10s"))
	if err != nil || reconcileTimeout <= 0 {
		return config{}, fmt.Errorf("RECONCILE_TIMEOUT must be a positive duration")
	}
	cfg := config{
		Mode: strings.ToLower(env("IOI_MODE", "observe")), Node: os.Getenv("NODE_NAME"),
		Namespace: env("TARGET_NAMESPACE", "cindy1-ioi"), ActiveCell: os.Getenv("ACTIVE_CELL"),
		DataMount: env("DATA_MOUNT", "/mnt/kubelet"), RootMount: env("ROOT_MOUNT", "/"),
		SysRoot:    env("SYS_ROOT", "/sys"),
		CgroupRoot: env("CGROUP_ROOT", "/sys/fs/cgroup"),
		Baseline:   uint32(baseline64), Poll: poll, ReconcileTimeout: reconcileTimeout,
		HTTPAddr: env("HTTP_ADDR", ":8080"),
	}
	if cfg.Mode != "observe" && cfg.Mode != "enforce" {
		return config{}, fmt.Errorf("IOI_MODE must be observe or enforce")
	}
	if cfg.Node == "" {
		return config{}, fmt.Errorf("NODE_NAME is required")
	}
	if cfg.Mode == "enforce" && cfg.ActiveCell == "" {
		return config{}, fmt.Errorf("ACTIVE_CELL is required in enforce mode")
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (c *controller) run(ctx context.Context) error {
	if _, err := c.client.CoreV1().Nodes().Get(ctx, c.cfg.Node, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("verify node %s: %w", c.cfg.Node, err)
	}
	ticker := time.NewTicker(c.cfg.Poll)
	defer ticker.Stop()

	for {
		// Every reconcile call gets its own bounded deadline, independent
		// of the process-lifetime ctx above. Without this, a single
		// stalled Kubernetes API call inside reconcile blocks this
		// for-loop from ever reaching the select below again -- the poll
		// ticker keeps firing but nothing consumes it, and no further
		// evidence of any kind is ever emitted again, silently defeating
		// the fail-closed contract every other check in this file is built
		// on. This was observed live: FAIL_CLOSED logged every ~2s, then
		// zero log lines of any kind for the rest of a run once conditions
		// should have allowed materialization to proceed (see
		// docs/experiment-runbook.md's gate-ordering note).
		//
		// Scope of this fix: it bounds the one context-aware call in
		// reconcile (the Pods().List() Kubernetes API call), which is the
		// most plausible cause of an indefinite, silent stall -- the other
		// steps (CheckControllerReady, DiscoverPodCgroup, the cgroup
		// writes/reads) are pure, fast, local syscalls with no known
		// blocking behavior, confirmed by direct source review. It does
		// NOT preempt a truly stuck syscall in those steps, since Go
		// cannot cancel a syscall that ignores its context; that residual
		// risk is why cmd/gate-wait exists as a fully independent barrier
		// that never depends on this adapter process being healthy at all.
		reconcileCtx, cancel := context.WithTimeout(ctx, c.cfg.ReconcileTimeout)
		err := c.reconcile(reconcileCtx)
		cancel()
		if err != nil {
			c.ready.Store(false)
			c.emit(evidence{Verdict: "FAIL_CLOSED", Error: err.Error()})
		}
		select {
		case <-ctx.Done():
			if c.active != nil {
				state, err := c.materializer.Rollback(*c.active)
				if err != nil {
					return fmt.Errorf("shutdown rollback not proven: %w", err)
				}
				c.emit(evidence{Verdict: "ROLLBACK_PROVEN", State: state})
			}
			return nil
		case <-ticker.C:
		}
	}
}

func (c *controller) reconcile(ctx context.Context) error {
	if err := iocostadapter.CheckControllerReady(c.cfg.CgroupRoot, c.cfg.Device, nil); err != nil {
		return fmt.Errorf("controller state not materialized: %w", err)
	}
	pods, err := c.client.CoreV1().Pods(c.cfg.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + c.cfg.Node,
		LabelSelector: cellLabel,
	})
	if err != nil {
		return fmt.Errorf("list experiment Pods: %w", err)
	}
	selected := make([]corev1.Pod, 0, 2)
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if c.cfg.ActiveCell != "" && pod.Labels[cellLabel] != c.cfg.ActiveCell {
			continue
		}
		if _, ok := pod.Annotations[weightAnnotation]; ok {
			selected = append(selected, pod)
		}
	}
	if len(selected) == 0 {
		c.ready.Store(c.cfg.Mode == "observe")
		return nil
	}
	if len(selected) != 2 {
		return fmt.Errorf("expected exactly two eligible Pods, found %d", len(selected))
	}
	if selected[0].Labels[cellLabel] == "" || selected[0].Labels[cellLabel] != selected[1].Labels[cellLabel] {
		return fmt.Errorf("eligible Pods do not share one cell identity")
	}

	plan, err := c.plan(selected)
	if err != nil {
		return err
	}
	if c.cfg.Mode == "observe" {
		state, err := c.materializer.Read(plan)
		if err != nil {
			return err
		}
		c.emit(evidence{Verdict: "OBSERVED", Cell: selected[0].Labels[cellLabel], State: state})
		return nil
	}

	// A restarted adapter may encounter a treatment it previously wrote.
	// Adopt it only when every independently read value exactly matches the
	// requested plan; otherwise Apply enforces the clean-baseline precondition.
	state, readErr := c.materializer.Read(plan)
	if readErr == nil && matches(plan, state, c.cfg.Device, c.cfg.Baseline) {
		c.active = &plan
		c.ready.Store(true)
		c.emit(evidence{Verdict: "MATERIALIZED_READBACK_PROVEN", Cell: selected[0].Labels[cellLabel], State: state})
		return nil
	}
	state, err = c.materializer.Apply(plan)
	if err != nil {
		return err
	}
	c.active = &plan
	c.ready.Store(true)
	c.emit(evidence{Verdict: "MATERIALIZED_READBACK_PROVEN", Cell: selected[0].Labels[cellLabel], State: state})
	return nil
}

func (c *controller) plan(pods []corev1.Pod) (iocostintent.Plan, error) {
	bindings := make(map[string]iocostintent.ApplicationBinding, 2)
	weights := make(map[string]uint32, 2)
	for _, pod := range pods {
		role := pod.Labels[roleLabel]
		if role != "app-a" && role != "app-b" {
			return iocostintent.Plan{}, fmt.Errorf("unsupported or duplicate role %q", role)
		}
		if _, exists := bindings[role]; exists {
			return iocostintent.Plan{}, fmt.Errorf("duplicate role %q", role)
		}
		weight64, err := strconv.ParseUint(pod.Annotations[weightAnnotation], 10, 32)
		if err != nil {
			return iocostintent.Plan{}, fmt.Errorf("Pod %s io-weight: %w", pod.Name, err)
		}
		path, err := iocostadapter.DiscoverPodCgroup(c.cfg.CgroupRoot, string(pod.UID))
		if err != nil {
			return iocostintent.Plan{}, fmt.Errorf("Pod %s cgroup: %w", pod.Name, err)
		}
		bindings[role] = iocostintent.ApplicationBinding{Application: role, PodUID: string(pod.UID), CgroupPath: path}
		weights[role] = uint32(weight64)
	}
	return iocostintent.BuildPlan(iocostintent.Intent{
		Device: c.cfg.Device, Protected: bindings["app-a"], Competing: bindings["app-b"],
		ProtectedWeight: weights["app-a"], CompetingWeight: weights["app-b"], BaselineWeight: c.cfg.Baseline,
	})
}

func matches(plan iocostintent.Plan, states []iocostadapter.EffectiveWeight, device string, baseline uint32) bool {
	if len(plan.Treatment) != len(states) {
		return false
	}
	want := make(map[string]uint32, len(plan.Treatment))
	for _, write := range plan.Treatment {
		weight, _, err := iocostintent.ParseDeviceWeight(fmt.Sprintf("default %d\n%s\n", baseline, write.Value), device)
		if err != nil {
			return false
		}
		want[write.PodUID] = weight
	}
	for _, state := range states {
		if !state.Override || want[state.PodUID] != state.Weight {
			return false
		}
	}
	return true
}

func (c *controller) emit(item evidence) {
	item.Time = time.Now().UTC().Format(time.RFC3339Nano)
	item.Mode, item.Node, item.Device = c.cfg.Mode, c.cfg.Node, c.cfg.Device
	b, _ := json.Marshal(item)
	klog.Info(string(b))
}

func (c *controller) serveHealth() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !c.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(c.cfg.HTTPAddr, mux); err != nil {
		klog.Fatalf("health server: %v", err)
	}
}
