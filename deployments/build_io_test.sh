#!/bin/bash
set -e

IMAGE_NAME="ioisolation/io-test:dev-fixed"

echo "🔨 1. Building io-test image..."
sudo docker build -f Dockerfile.io-test -t $IMAGE_NAME .

echo "📦 2. Saving image as tar..."
docker save -o io-test.tar $IMAGE_NAME

echo "📥 3. Importing into containerd..."
sudo /home/cindyli/code/containerd/bin/ctr --namespace k8s.io images import io-test.tar

echo "🚀 4. Re-deploying Pod using new image..."
kubectl delete pod io-network-test -n ioi-system --ignore-not-found
sleep 2
kubectl run io-network-test \
  -n ioi-system \
  --image=$IMAGE_NAME \
  --restart=Never \
  --overrides='
{
  "apiVersion": "v1",
  "spec": {
    "containers": [{
      "name": "io-tester",
      "image": "'$IMAGE_NAME'",
      "resources": {
        "limits": {
          "cpu": "500m",
          "memory": "256Mi",
          "io/network.mbps": "10",
          "io/disk.mbps": "20"
        },
        "requests": {
          "cpu": "500m",
          "memory": "256Mi",
          "io/network.mbps": "10",
          "io/disk.mbps": "20"
        }
      },
      "volumeMounts": [{
        "mountPath": "/tmp",
        "name": "tmp-storage"
      }]
    }],
    "volumes": [{
      "name": "tmp-storage",
      "emptyDir": {}
    }]
  }
}'
