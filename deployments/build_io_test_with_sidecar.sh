#!/bin/bash
set -e

# === Config ===
IO_IMAGE="ioisolation/io-test:dev-fixed"
EBPF_TAG="local-$(date +%s)"
EBPF_IMAGE="localhost/pinc-ebpf-agent:$EBPF_TAG"
TARGET_NS="ioi-system"
POD_NAME="io-network-test"
FINAL_MANIFEST="io-network-test-sidecar.yaml"
CTR_PATH="/home/cindyli/code/containerd/bin/ctr"

echo "🔨 [1] Building io-tester image..."
sudo docker build -f Dockerfile.io-test -t $IO_IMAGE .

echo "📦 [2] Saving io-tester image..."
docker save -o io-test.tar $IO_IMAGE

echo "📥 [3] Importing io-tester into containerd..."
sudo $CTR_PATH --namespace k8s.io images import io-test.tar

echo "🔨 [4] Rebuilding eBPF Agent image with tag: $EBPF_TAG"
cd /home/cindyli/code/pinc-ebpf-agent
make docker-build IMAGE_NAME=pinc-ebpf-agent:latest
docker tag pinc-ebpf-agent:latest $EBPF_IMAGE

echo "📦 [5] Saving eBPF Agent image..."
docker save -o pinc-ebpf-agent.tar $EBPF_IMAGE

echo "📥 [6] Importing eBPF Agent into containerd..."
sudo $CTR_PATH --namespace k8s.io images import pinc-ebpf-agent.tar

echo "📄 [7] Generating pod manifest with eBPF sidecar..."
cd /home/cindyli/code/cloud-resource-scheduling-and-isolation/deployments/
cat > $FINAL_MANIFEST <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME
  namespace: $TARGET_NS
spec:
  hostPID: true
  hostNetwork: true
  containers:
    - name: io-tester
      image: $IO_IMAGE
      resources:
        requests:
          cpu: "500m"
          memory: "256Mi"
          io/disk.mbps: "20"
          io/network.mbps: "10"
        limits:
          cpu: "500m"
          memory: "256Mi"
          io/disk.mbps: "20"
          io/network.mbps: "10"
      volumeMounts:
        - mountPath: /tmp
          name: tmp-storage
    - name: ebpf-agent
      image: $EBPF_IMAGE
      imagePullPolicy: IfNotPresent
      securityContext:
        privileged: true
      env:
        - name: ZIPKIN_URL
          value: "http://10.9.220.244:9411/api/v2/spans"
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
        - name: ZIPKIN_URL
          value: "http://10.9.220.244:9411/api/v2/spans"
      resources:
        requests:
          cpu: 50m
          memory: 64Mi
        limits:
          cpu: 200m
          memory: 256Mi
      volumeMounts:
        - name: sys
          mountPath: /sys
          readOnly: true
        - name: proc
          mountPath: /proc
          readOnly: true
        - name: kubeconfig
          mountPath: /root/.kube/config
          readOnly: true
  volumes:
    - name: tmp-storage
      emptyDir: {}
    - name: sys
      hostPath:
        path: /sys
        type: Directory
    - name: proc
      hostPath:
        path: /proc
        type: Directory
    - name: kubeconfig
      hostPath:
        path: /home/cindyli/.kube/config
        type: File
EOF

echo "🧹 [8] Deleting old Pod (if any)..."
kubectl delete pod $POD_NAME -n $TARGET_NS --ignore-not-found
sleep 2

echo "🚀 [9] Deploying new Pod with sidecar..."
kubectl apply -f $FINAL_MANIFEST

echo "✅ [Done] Pod '$POD_NAME' deployed with latest eBPF agent: $EBPF_IMAGE"
