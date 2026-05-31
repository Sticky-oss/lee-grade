#!/usr/bin/env bash
# Stand up a 2-node Ansible managed-node topology for the RHCE/EX294 track,
# entirely inside one Rocky VM using ROOTFUL podman: the VM is the control
# node; node1 and node2 are systemd+sshd Rocky containers on a 'leenet' bridge,
# reachable by IP from the VM over real SSH (key auth) — the authentic EX294
# shape with no extra VMs (fits a 16GB laptop) and ansible-core only (no Galaxy
# collection). Writes a ready inventory + ansible.cfg under /home/lee/multinode
# and proves Ansible reaches both nodes. Idempotent: safe to re-run.
#
# After this, grade fleet idempotency with the existing ansible-playbook check:
#   cp tasks/rhce-9/solutions/multinode/site.yml /home/lee/multinode/site.yml
#   sudo lee-grade --task tasks/rhce-9/ansible-multinode-demo.yaml
set -uo pipefail
MN=/home/lee/multinode
mkdir -p "$MN"

echo "########## install podman + ssh client ##########"
if ! command -v podman >/dev/null; then
  sudo dnf install -y podman openssh-clients >/tmp/podman-install.log 2>&1 || { echo "dnf install FAILED"; tail -25 /tmp/podman-install.log; exit 1; }
fi
podman --version

echo "########## ssh keypair (control -> nodes) ##########"
if [ ! -f "$MN/ansible_node" ]; then
  ssh-keygen -t ed25519 -N "" -f "$MN/ansible_node" -C "lee-grade-ansible-control" >/dev/null
fi
cp "$MN/ansible_node.pub" "$MN/authorized_keys"

echo "########## Containerfile ##########"
cat > "$MN/Containerfile" <<'CF'
FROM quay.io/rockylinux/rockylinux:9
RUN dnf -y install systemd openssh-server sudo python3 procps-ng iproute && \
    dnf clean all && \
    systemctl enable sshd && \
    useradd -m -s /bin/bash ansible && \
    echo 'ansible ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ansible && \
    chmod 440 /etc/sudoers.d/ansible && \
    mkdir -p /home/ansible/.ssh && chmod 700 /home/ansible/.ssh
COPY authorized_keys /home/ansible/.ssh/authorized_keys
RUN chmod 600 /home/ansible/.ssh/authorized_keys && \
    chown -R ansible:ansible /home/ansible/.ssh && \
    ssh-keygen -A
CMD ["/sbin/init"]
CF

echo "########## build node image ##########"
sudo podman build -t lee-node "$MN" >/tmp/podman-build.log 2>&1 || { echo BUILD-FAILED; tail -30 /tmp/podman-build.log; exit 1; }
echo IMAGE-BUILT

echo "########## network + nodes ##########"
sudo podman network exists leenet || sudo podman network create leenet >/dev/null
for n in node1 node2; do
  sudo podman rm -f "$n" >/dev/null 2>&1 || true
  # --cap-add=AUDIT_WRITE: OpenSSH fatally exits a session if it cannot write a
  # login record to the kernel audit subsystem; containers drop that cap by
  # default, which closes the connection right after the key is accepted.
  sudo podman run -d --name "$n" --hostname "$n" --systemd=always \
    --cap-add=AUDIT_WRITE --network leenet lee-node >/dev/null
done
sudo podman ps --format '{{.Names}}  {{.Status}}'

echo "########## wait for sshd + collect IPs ##########"
: > "$MN/inventory_hosts"
for n in node1 node2; do
  ip=$(sudo podman inspect -f '{{.NetworkSettings.Networks.leenet.IPAddress}}' "$n")
  if [ -z "$ip" ]; then
    echo "multinode-setup: $n has no IP on the leenet network — aborting (check: sudo podman logs $n)" >&2
    exit 1
  fi
  for i in $(seq 1 30); do
    if ssh -n -i "$MN/ansible_node" -o StrictHostKeyChecking=no -o ConnectTimeout=2 -o BatchMode=yes "ansible@$ip" true 2>/dev/null; then break; fi
    sleep 1
  done
  echo "$n ansible_host=$ip" >> "$MN/inventory_hosts"
done

echo "########## inventory + ansible.cfg ##########"
{
  echo "[managed]"
  cat "$MN/inventory_hosts"
  echo
  echo "[managed:vars]"
  echo "ansible_user=ansible"
  echo "ansible_ssh_private_key_file=$MN/ansible_node"
  echo "ansible_python_interpreter=/usr/bin/python3"
} > "$MN/inventory"
cat > "$MN/ansible.cfg" <<'CFG'
[defaults]
inventory = inventory
host_key_checking = False
CFG
echo "--- inventory ---"
cat "$MN/inventory"

echo "########## hosts.yaml (for lee-grade --hosts, remote checks) ##########"
{
  echo "hosts:"
  while read -r n hostspec; do
    ip="${hostspec#ansible_host=}"
    echo "  $n:"
    echo "    address: $ip"
    echo "    user: ansible"
    echo "    key: $MN/ansible_node"
  done < "$MN/inventory_hosts"
} > "$MN/hosts.yaml"
cat "$MN/hosts.yaml"

echo "########## connectivity test (ansible ping) ##########"
( cd "$MN" && ansible managed -m ping -o )

echo MULTINODE-SETUP-DONE
