#!/bin/bash
set -e

IMAGE_NAME="ioisolation/node-agent:dev-final"

echo "🛠️ 1. Rebuilding node-agent binary..."
sudo make node-agent

echo "📦 2. Building Docker image: $IMAGE_NAME"
sudo docker build -f ./build/node-agent/Dockerfile -t $IMAGE_NAME .

echo "📦 3. Saving Docker image to TAR..."
docker save -o node-agent.tar $IMAGE_NAME

echo "📦 4. Importing image into containerd..."
sudo /home/cindyli/code/containerd/bin/ctr --namespace k8s.io images import node-agent.tar

echo "🚀 5. Updating DaemonSet to use image: $IMAGE_NAME"
kubectl -n ioi-system set image daemonset/node-agent-daemon node-agent=$IMAGE_NAME

echo "🔁 6. Rolling out DaemonSet restart..."
kubectl -n ioi-system rollout restart daemonset node-agent-daemon


echo "✅ DONE. Watch logs with:"
echo "   kubectl logs -n ioi-system -l app=node-agent -f"
