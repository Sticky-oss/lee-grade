#!/usr/bin/env bash
# Comprehensive worked solution (answer key) for the RHCSA-9 FULL practice exam
# (all twelve tasks). Run as root on the lab VM. Idempotent: safe to re-run.
set -uo pipefail

echo "[deps]"
dnf install -y httpd cronie policycoreutils-python-utils xfsprogs firewalld \
               tuned chrony acl >/dev/null 2>&1 || true
systemctl enable --now crond >/dev/null 2>&1 || true

echo "[users-sudo]"
groupadd -g 4000 dbadmins 2>/dev/null || true
id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba
usermod -s /bin/bash -aG wheel,dbadmins dba
echo '%dbadmins ALL=(ALL) ALL' > /etc/sudoers.d/dbadmins
chmod 0440 /etc/sudoers.d/dbadmins

echo "[shared-dir]"
mkdir -p /srv/teamdata; chgrp dbadmins /srv/teamdata; chmod 2770 /srv/teamdata

echo "[cron]"
printf '#!/usr/bin/env bash\necho "backup $(date)" >> /var/log/backup.log\n' > /usr/local/bin/backup.sh
chmod 0755 /usr/local/bin/backup.sh
echo '0 3 * * * /usr/local/bin/backup.sh' | crontab -u dba -

echo "[web-firewall]"
systemctl enable --now httpd >/dev/null 2>&1
systemctl enable --now firewalld >/dev/null 2>&1 || true
firewall-cmd --permanent --add-service=http >/dev/null 2>&1 || true
firewall-cmd --reload >/dev/null 2>&1 || true

echo "[selinux]"
setenforce 1 2>/dev/null || true
sed -i 's/^SELINUX=.*/SELINUX=enforcing/' /etc/selinux/config 2>/dev/null || true
mkdir -p /srv/web
semanage fcontext -a -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null \
  || semanage fcontext -m -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null || true
restorecon -R /srv/web 2>/dev/null || true
setsebool -P httpd_can_network_connect on 2>/dev/null || true

echo "[storage]"
mkdir -p /mnt/data
if [ ! -f /var/lib/lee-disk.img ]; then
  dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none
  mkfs.xfs -q /var/lib/lee-disk.img
fi
mountpoint -q /mnt/data || mount -o loop /var/lib/lee-disk.img /mnt/data
grep -q '[[:space:]]/mnt/data[[:space:]]' /etc/fstab || \
  echo '/var/lib/lee-disk.img /mnt/data xfs loop 0 0' >> /etc/fstab

echo "[boot-target]"
systemctl set-default multi-user.target >/dev/null 2>&1 || true

echo "[time]"
systemctl enable --now chronyd >/dev/null 2>&1 || true
timedatectl set-timezone America/Toronto 2>/dev/null || true

echo "[tuned]"
systemctl enable --now tuned >/dev/null 2>&1 || true
tuned-adm profile virtual-guest 2>/dev/null || true

echo "[swap]"
if ! swapon --show=NAME --noheadings | grep -q '/swapfile'; then
  [ -f /swapfile ] || dd if=/dev/zero of=/swapfile bs=1M count=256 status=none
  chmod 0600 /swapfile
  mkswap /swapfile >/dev/null 2>&1 || true
  swapon /swapfile 2>/dev/null || true
fi
grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap defaults 0 0' >> /etc/fstab

echo "[journald]"
mkdir -p /var/log/journal
if grep -q '^#\?Storage=' /etc/systemd/journald.conf; then
  sed -i 's/^#\?Storage=.*/Storage=persistent/' /etc/systemd/journald.conf
else
  sed -i '/^\[Journal\]/a Storage=persistent' /etc/systemd/journald.conf
fi
systemctl restart systemd-journald >/dev/null 2>&1 || true

echo "[acl]"
mkdir -p /srv/reports
setfacl -m u:dba:rwx /srv/reports 2>/dev/null || true

echo SOLVE-DONE
