// ioi-capability-scheduler is a stock kube-scheduler binary with exactly
// one added out-of-tree Filter plugin (iocostschedulerplugin). It never
// replaces or patches the cluster's default scheduler; Pods reach it
// only by explicitly setting spec.schedulerName to this binary's
// configured profile name.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/component-base/cli"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"sigs.k8s.io/IOIsolation/scheduler-plugin/pkg/iocostschedulerplugin"
)

func main() {
	args := iocostschedulerplugin.Args{
		Namespace:     env("CAPABILITY_NAMESPACE", ""),
		ConfigMapName: env("CAPABILITY_CONFIGMAP", "ioi-capability-inventory"),
		MaxAge:        envDuration("CAPABILITY_MAX_AGE", 20*time.Minute),
	}
	if args.Namespace == "" {
		fmt.Fprintln(os.Stderr, "ioi-capability-scheduler: CAPABILITY_NAMESPACE is required")
		os.Exit(1)
	}

	command := app.NewSchedulerCommand(
		app.WithPlugin(iocostschedulerplugin.Name, iocostschedulerplugin.NewFactory(args)),
	)
	code := cli.Run(command)
	os.Exit(code)
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return fallback
}
