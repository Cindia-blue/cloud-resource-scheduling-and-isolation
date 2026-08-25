/*
Copyright (C) 2025 Intel Corporation
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"os"
	"os/signal"

	"k8s.io/klog/v2"
	"sigs.k8s.io/IOIsolation/pkg/service"
	_ "sigs.k8s.io/IOIsolation/pkg/service/disk"
)

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	klog.Infof("Service started.")
	flag.Parse()

	as := service.GetService()
	if err := as.Run(); err != nil {
		klog.Warning(err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	klog.Info("Stop Service")
	if err := as.Close(); err != nil {
		klog.Warning(err)
		os.Exit(1)
	}
}
