#!/bin/bash
set -e

echo "Starting IO test loop..."
ITERATION=1

while true; do
  echo "=== [Round $ITERATION] Disk IO Test ==="
  fio --name=test --filename=/tmp/testfile --filesize=512M --bs=1M --rw=write --ioengine=libaio --iodepth=1 --direct=1 || {
    echo "❌ FIO test failed in round $ITERATION"
  }

  echo "=== [Round $ITERATION] Network IO Test ==="
  echo "Pinging iperf3-server..."
  ping -c 4 10.244.0.48 || {
    echo "❌ Ping to iperf3-server failed in round $ITERATION"
  }

  echo "Running iperf3..."
  iperf3 -c 10.244.0.48 -t 10 || {
    echo "❌ iperf3 test failed in round $ITERATION"
  }

  echo "=== Round $ITERATION Done ==="
  ITERATION=$((ITERATION + 1))

  sleep 30
done
