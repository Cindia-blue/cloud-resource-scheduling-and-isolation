#!/bin/sh
set -e

echo "📦 [run.sh] Running node-agent with --kubeconfig=/etc/kubernetes/kubelet.conf"
exec /bin/node-agent --v=2 --kubeconfig=/etc/kubernetes/kubelet.conf
