#!/usr/bin/env bash
# Worked solution (answer key) for the RHCSA-9 sample exam. Run as root on the
# lab VM. Idempotent: safe to re-run. Each block corresponds to one task.
set -uo pipefail

echo "[deps]"
dnf install -y httpd cronie policycoreutils-python-utils xfsprogs firewalld >/dev/null 2>&1 || true
systemctl enable --now crond >/dev/null 2>&1 || true

echo "[rhcsa-users-sudo]"
groupadd -g 4000 dbadmins 2>/dev/null || true
id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba
usermod -s /bin/bash -aG wheel,dbadmins dba
echo '%dbadmins ALL=(ALL) ALL' > /etc/sudoers.d/dbadmins
chmod 0440 /etc/sudoers.d/dbadmins

echo "[rhcsa-shared-dir]"
mkdir -p /srv/teamdata
chgrp dbadmins /srv/teamdata
chmod 2770 /srv/teamdata

echo "[rhcsa-cron]"
cat > /usr/local/bin/backup.sh <<'EOS'
#!/usr/bin/env bash
echo "backup $(date)" >> /var/log/backup.log
EOS
chmod 0755 /usr/local/bin/backup.sh
echo '0 3 * * * /usr/local/bin/backup.sh' | crontab -u dba -

echo "[rhcsa-web-firewall]"
systemctl enable --now httpd >/dev/null 2>&1
systemctl enable --now firewalld >/dev/null 2>&1 || true
firewall-cmd --permanent --add-service=http >/dev/null 2>&1 || true
firewall-cmd --reload >/dev/null 2>&1 || true

echo "[rhcsa-selinux]"
setenforce 1 2>/dev/null || true
sed -i 's/^SELINUX=.*/SELINUX=enforcing/' /etc/selinux/config 2>/dev/null || true
mkdir -p /srv/web
semanage fcontext -a -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null \
  || semanage fcontext -m -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null || true
restorecon -R /srv/web 2>/dev/null || true
setsebool -P httpd_can_network_connect on 2>/dev/null || true

echo "[rhcsa-storage]"
mkdir -p /mnt/data
if [ ! -f /var/lib/lee-disk.img ]; then
  dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none
  mkfs.xfs -q /var/lib/lee-disk.img
fi
mountpoint -q /mnt/data || mount -o loop /var/lib/lee-disk.img /mnt/data
grep -q '[[:space:]]/mnt/data[[:space:]]' /etc/fstab || \
  echo '/var/lib/lee-disk.img /mnt/data xfs loop 0 0' >> /etc/fstab

echo SOLVE-DONE
