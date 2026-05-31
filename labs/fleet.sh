#!/usr/bin/env bash
# fleet — CALYX field-ops interface. Grade the managed-node cluster (node1,
# node2) over SSH, or check it's alive. Installed on the VM as /usr/local/bin/fleet.
set -uo pipefail
BIN=/home/lee/payload/lee-grade
HOSTS=/home/lee/multinode/hosts.yaml
MN=/home/lee/multinode
TASKS=/home/lee/payload/tasks

if [ ! -f "$HOSTS" ]; then
  echo "fleet: no managed-node cluster provisioned."
  echo "       deploy it with: bash ~/lee-grade/labs/multinode-setup.sh  (or labs/multinode-setup.sh)"
  exit 2
fi

verb=${1:-grade}
case "$verb" in
  ""|grade)     "$BIN" --task "$TASKS/ansible-multinode-fleet-demo.yaml" --hosts "$HOSTS" ;;
  web)          "$BIN" --task "$TASKS/ansible-multinode-web-demo.yaml" --hosts "$HOSTS" ;;
  ping|status)  ( cd "$MN" && ansible managed -m ping ) ;;
  *)            echo "usage: fleet {grade|web|ping}" ;;
esac
