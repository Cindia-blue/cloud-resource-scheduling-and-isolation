/*
Copyright (C) 2025 Intel Corporation
SPDX-License-Identifier: Apache-2.0
*/

package net

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"sigs.k8s.io/IOIsolation/generated/ioi/clientset/versioned"
	"sigs.k8s.io/IOIsolation/pkg/agent"
)

type NetEngine struct {
	agent.IOEngine
	nodeName   string
	tcApplied  map[string]bool
}

func (e *NetEngine) Type() string {
	return "NetIO"
}

func (e *NetEngine) Initialize(coreClient *kubernetes.Clientset, client *versioned.Clientset, mtls bool) error {
	klog.Info("[NetEngine] ✅ Using nsenter + ifindex strategy with looped detection")

	if !agent.CheckEngine(agent.NetSwitch) {
		klog.Info("[NetEngine] NetSwitch not enabled, skipping NetEngine")
		return nil
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %v", err)
	}
	e.nodeName = hostname
	e.tcApplied = make(map[string]bool)

	go func() {
		for {
			e.detectAndApplyTc(coreClient)
			time.Sleep(30 * time.Second)
		}
	}()

	return nil
}

func (e *NetEngine) Uninitialize() error {
	return nil
}

func (e *NetEngine) Enforce(pod interface{}) error {
	return nil
}

func (e *NetEngine) detectAndApplyTc(client *kubernetes.Clientset) {
	pods, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.phase=Running", e.nodeName),
	})
	if err != nil {
		klog.Errorf("Failed to list pods: %v", err)
		return
	}

	for _, pod := range pods.Items {
		uid := string(pod.UID)
		if e.tcApplied[uid] {
			continue
		}
		if pod.Namespace == "kube-system" || pod.Spec.HostNetwork {
			continue
		}
		if !hasNetworkAnnotationOrResource(&pod) {
			continue
		}
		rate := extractNetworkLimitMbps(&pod)
		if rate == "" {
			rate = "10mbit"
		}
		pid, err := getPodPidFromContainerd(&pod)
		if err != nil {
			klog.Infof("[TC] Retry later: Cannot get PID for pod %s/%s: %v", pod.Namespace, pod.Name, err)
			continue
		}
		klog.Infof("[TC] Pod %s/%s -> PID %s", pod.Namespace, pod.Name, pid)
		veth, err := findVethByNsenter(pid)
		if err != nil {
			klog.Infof("[TC] Retry later: Cannot find veth for pod %s/%s (PID %s): %v", pod.Namespace, pod.Name, pid, err)
			continue
		}
		klog.Infof("[TC] Pod %s/%s uses veth %s", pod.Namespace, pod.Name, veth)
		err = applyTcWithIngress(veth, rate)
		if err != nil {
			klog.Warningf("[TC] Failed to apply tc on %s: %v", veth, err)
		} else {
			e.tcApplied[uid] = true
			klog.Infof("[TC] ✅ Applied tc to pod %s/%s on device %s with rate %s", pod.Namespace, pod.Name, veth, rate)
		}
	}
}

func hasNetworkAnnotationOrResource(pod *v1.Pod) bool {
	if val, ok := pod.Annotations["networkio.kubernetes.io/resources"]; ok && val != "" {
		return true
	}
	for _, container := range pod.Spec.Containers {
		if _, ok := container.Resources.Limits["io/network.mbps"]; ok {
			return true
		}
		if _, ok := container.Resources.Requests["io/network.mbps"]; ok {
			return true
		}
	}
	return false
}

func extractNetworkLimitMbps(pod *v1.Pod) string {
	for _, container := range pod.Spec.Containers {
		if val, ok := container.Resources.Limits["io/network.mbps"]; ok {
			qty := val.String()
			if _, err := resource.ParseQuantity(qty); err == nil {
				return qty + "mbit"
			}
		}
	}
	return ""
}

func getPodPidFromContainerd(pod *v1.Pod) (string, error) {
	for _, status := range pod.Status.ContainerStatuses {
		cid := extractCID(status.ContainerID)
		if cid == "" {
			continue
		}
		procDirs, _ := filepath.Glob("/proc/[0-9]*/cgroup")
		for _, cgroupFile := range procDirs {
			data, err := ioutil.ReadFile(cgroupFile)
			if err != nil {
				continue
			}
			if strings.Contains(string(data), cid) {
				parts := strings.Split(cgroupFile, "/")
				if len(parts) >= 3 {
					return parts[2], nil
				}
			}
		}
	}
	return "", fmt.Errorf("no PID found for pod %s", pod.Name)
}

func extractCID(containerID string) string {
	parts := strings.Split(containerID, "://")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func findVethByNsenter(pid string) (string, error) {
	cmd := exec.Command("nsenter", "-t", pid, "-n", "ip", "link", "show", "eth0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nsenter failed: %v, output: %s", err, string(out))
	}
	klog.Infof("[DEBUG] nsenter output for PID %s:\n%s", pid, string(out))

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "@if") {
			continue
		}
		idxStart := strings.Index(line, "@if")
		if idxStart == -1 {
			continue
		}
		idxStr := strings.Fields(line[idxStart+3:])[0]
		idxStr = strings.TrimSuffix(idxStr, ":")
		klog.Infof("[DEBUG] Extracted ifindex: %s", idxStr)
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		entries, _ := filepath.Glob("/sys/class/net/*/ifindex")
		for _, path := range entries {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				continue
			}
			content := strings.TrimSpace(string(data))
			id, _ := strconv.Atoi(content)
			if id == idx {
				iface := filepath.Base(filepath.Dir(path))
				klog.Infof("[DEBUG] Matched host veth %s (ifindex %d)", iface, id)
				return iface, nil
			}
		}
	}
	return "", fmt.Errorf("eth0 ifindex not found")
}

func applyTcWithIngress(iface, rate string) error {
	exec.Command("tc", "qdisc", "del", "dev", iface, "root").Run()
	cmd := exec.Command("tc", "qdisc", "add", "dev", iface, "root", "tbf", "rate", rate, "burst", "32kbit", "latency", "400ms")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc root error: %v, output: %s", err, string(out))
	}

	exec.Command("tc", "qdisc", "del", "dev", iface, "ingress").Run()
	cmd = exec.Command("tc", "qdisc", "add", "dev", iface, "ingress")
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc ingress qdisc error: %v, output: %s", err, string(out))
	}

	cmd = exec.Command("tc", "filter", "add", "dev", iface, "parent", "ffff:", "protocol", "ip", "prio", "1",
		"u32", "match", "ip", "dst", "0.0.0.0/0", "police", "rate", rate, "burst", "32k", "drop", "flowid", ":1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc ingress filter error: %v, output: %s", err, string(out))
	}

	return nil
}

func init() {
	a := agent.GetAgent()
	a.RegisterEngine(&NetEngine{
		IOEngine: agent.IOEngine{
			Flag:          0,
			ExecutionFlag: agent.ProfileFlag | agent.PolicyFlag | agent.AdminFlag,
		},
	})
}