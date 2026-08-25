package main

import (
	"flag"
	"os"
	"os/signal"

	"k8s.io/klog/v2"
	"sigs.k8s.io/IOIsolation/pkg/agent"

	_ "sigs.k8s.io/IOIsolation/pkg/agent/common"
	_ "sigs.k8s.io/IOIsolation/pkg/agent/disk"
	_ "sigs.k8s.io/IOIsolation/pkg/agent/engines"
	_ "sigs.k8s.io/IOIsolation/pkg/agent/metrics"
)

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Absolute path to kubeconfig file (default is in-cluster config)")
	flag.Parse()

	if kubeconfig != "" {
		klog.Infof("Using kubeconfig from: %s", kubeconfig)
		if err := os.Setenv("KUBECONFIG", kubeconfig); err != nil {
			klog.Fatalf("failed to set KUBECONFIG: %v", err)
		}
	} else {
		klog.Info("No kubeconfig provided, using in-cluster config")
	}

	klog.Info("1. node agent main start.")
	ag := agent.GetAgent()

	go func() {
		klog.Info("2. node agent run start.")
		err := agent.GetAgent().Run()
		if err != nil {
			klog.Warning(err)
			os.Exit(1)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	klog.Warning("Going to stop agent")
	err := ag.Stop()
	if err != nil {
		klog.Warning(err)
		os.Exit(1)
	}
}
