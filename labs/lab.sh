#!/usr/bin/env bash
# lab — Red Hat-style per-task practice harness for the lee-grade RHCSA track.
# Installed on the lab VM as /usr/local/bin/lab.
#
#   lab list                     show the available tasks
#   lab start  <task>            reset to a clean slate + lay down prerequisites
#   lab grade  <task>            evaluate your work on that task
#   lab solve  <task>            apply the worked solution (answer key)
#   lab finish <task>            tear the task's artifacts back to baseline
#   lab reset                    return EVERY task to baseline
#
# Each task knows how to set itself up (a fresh, attemptable starting state) and
# how to clean itself up (remove every artifact it touches), so practice is
# repeatable and isolated — the way rht's `lab` command works.
set -uo pipefail

BIN=/home/lee/payload/lee-grade
TASKDIR=/home/lee/payload/rhcsa-tasks
TASKS=(users-sudo shared-dir cron web-firewall selinux storage boot-target time tuned swap journald acl)

C=$'\e[96m'; G=$'\e[92m'; Y=$'\e[93m'; R=$'\e[0m'; B=$'\e[1m'; D=$'\e[2m'
die(){ echo "lab: $*" >&2; exit 2; }

# lab mutates system state — re-exec under sudo when not already root.
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi

taskfile(){ echo "$TASKDIR/rhcsa-$1.yaml"; }
known(){ local t; for t in "${TASKS[@]}"; do [ "$t" = "$1" ] && return 0; done; return 1; }
fn(){ echo "t_${1//-/_}_$2"; }

# ───────────────────────────── per-task hooks ─────────────────────────────
# _cleanup: artifacts -> baseline.  _setup: prereqs for a fresh attempt
# (defaults to _cleanup).  _solve: the answer key.

t_users_sudo_cleanup(){ userdel -r dba 2>/dev/null; groupdel dbadmins 2>/dev/null; rm -f /etc/sudoers.d/dbadmins; true; }
t_users_sudo_solve(){ groupadd -g 4000 dbadmins 2>/dev/null; id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba; usermod -s /bin/bash -aG wheel,dbadmins dba; echo '%dbadmins ALL=(ALL) ALL' >/etc/sudoers.d/dbadmins; chmod 0440 /etc/sudoers.d/dbadmins; }

t_shared_dir_cleanup(){ rm -rf /srv/teamdata; groupdel dbadmins 2>/dev/null; true; }
t_shared_dir_setup(){ t_shared_dir_cleanup; groupadd -g 4000 dbadmins 2>/dev/null; true; }
t_shared_dir_solve(){ mkdir -p /srv/teamdata; chgrp dbadmins /srv/teamdata; chmod 2770 /srv/teamdata; }

t_cron_cleanup(){ crontab -u dba -r 2>/dev/null; rm -f /usr/local/bin/backup.sh; userdel -r dba 2>/dev/null; true; }
t_cron_setup(){ t_cron_cleanup; id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba; true; }
t_cron_solve(){ printf '#!/usr/bin/env bash\necho "backup $(date)" >> /var/log/backup.log\n' >/usr/local/bin/backup.sh; chmod 0755 /usr/local/bin/backup.sh; echo '0 3 * * * /usr/local/bin/backup.sh' | crontab -u dba -; }

t_web_firewall_cleanup(){ systemctl disable --now httpd >/dev/null 2>&1; dnf remove -y httpd >/dev/null 2>&1; firewall-cmd --permanent --remove-service=http >/dev/null 2>&1; firewall-cmd --reload >/dev/null 2>&1; true; }
t_web_firewall_setup(){ systemctl enable --now firewalld >/dev/null 2>&1; t_web_firewall_cleanup; true; }
t_web_firewall_solve(){ dnf install -y httpd >/dev/null 2>&1; systemctl enable --now httpd >/dev/null 2>&1; firewall-cmd --permanent --add-service=http >/dev/null 2>&1; firewall-cmd --reload >/dev/null 2>&1; }

t_selinux_cleanup(){ rm -rf /srv/web; semanage fcontext -d '/srv/web(/.*)?' 2>/dev/null; setsebool -P httpd_can_network_connect off 2>/dev/null; true; }
t_selinux_solve(){ setenforce 1 2>/dev/null; mkdir -p /srv/web; semanage fcontext -a -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null || semanage fcontext -m -t httpd_sys_content_t '/srv/web(/.*)?' 2>/dev/null; restorecon -R /srv/web 2>/dev/null; setsebool -P httpd_can_network_connect on 2>/dev/null; }

t_storage_cleanup(){ umount /mnt/data 2>/dev/null; sed -i '\#/mnt/data#d' /etc/fstab; rm -rf /mnt/data /var/lib/lee-disk.img; true; }
t_storage_setup(){ t_storage_cleanup; dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none; true; }   # provide an unformatted "disk"
t_storage_solve(){ [ -f /var/lib/lee-disk.img ] || dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none; blkid /var/lib/lee-disk.img >/dev/null 2>&1 || mkfs.xfs -q /var/lib/lee-disk.img; mkdir -p /mnt/data; mountpoint -q /mnt/data || mount -o loop /var/lib/lee-disk.img /mnt/data; grep -q '[[:space:]]/mnt/data[[:space:]]' /etc/fstab || echo '/var/lib/lee-disk.img /mnt/data xfs loop 0 0' >>/etc/fstab; }

t_boot_target_cleanup(){ systemctl set-default multi-user.target >/dev/null 2>&1; true; }   # baseline = text
t_boot_target_setup(){ systemctl set-default graphical.target >/dev/null 2>&1; true; }       # clean slate = not-yet-done
t_boot_target_solve(){ systemctl set-default multi-user.target >/dev/null 2>&1; }

t_time_cleanup(){ timedatectl set-timezone UTC 2>/dev/null; true; }
t_time_solve(){ systemctl enable --now chronyd >/dev/null 2>&1; timedatectl set-timezone America/Toronto 2>/dev/null; }

t_tuned_cleanup(){ systemctl disable --now tuned >/dev/null 2>&1; dnf remove -y tuned >/dev/null 2>&1; true; }
t_tuned_solve(){ dnf install -y tuned >/dev/null 2>&1; systemctl enable --now tuned >/dev/null 2>&1; tuned-adm profile virtual-guest 2>/dev/null; }

t_swap_cleanup(){ swapoff /swapfile 2>/dev/null; sed -i '\#/swapfile#d' /etc/fstab; rm -f /swapfile; true; }
t_swap_solve(){ [ -f /swapfile ] || dd if=/dev/zero of=/swapfile bs=1M count=256 status=none; chmod 0600 /swapfile; swapon --show=NAME --noheadings | grep -q /swapfile || { mkswap /swapfile >/dev/null 2>&1; swapon /swapfile 2>/dev/null; }; grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap defaults 0 0' >>/etc/fstab; }

t_journald_cleanup(){ sed -i 's/^Storage=persistent/#Storage=auto/' /etc/systemd/journald.conf 2>/dev/null; rm -rf /var/log/journal; systemctl restart systemd-journald >/dev/null 2>&1; true; }
t_journald_solve(){ mkdir -p /var/log/journal; if grep -q '^#\?Storage=' /etc/systemd/journald.conf; then sed -i 's/^#\?Storage=.*/Storage=persistent/' /etc/systemd/journald.conf; else sed -i '/^\[Journal\]/a Storage=persistent' /etc/systemd/journald.conf; fi; systemctl restart systemd-journald >/dev/null 2>&1; }

t_acl_cleanup(){ rm -rf /srv/reports; userdel -r dba 2>/dev/null; true; }
t_acl_setup(){ t_acl_cleanup; id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba; true; }
t_acl_solve(){ mkdir -p /srv/reports; setfacl -m u:dba:rwx /srv/reports 2>/dev/null; }

# ───────────────────────────── verbs ─────────────────────────────
do_cleanup(){ local f; f=$(fn "$1" cleanup); declare -F "$f" >/dev/null && "$f"; true; }
do_setup(){ local f; f=$(fn "$1" setup); if declare -F "$f" >/dev/null; then "$f"; else do_cleanup "$1"; fi; }
do_solve(){ local f; f=$(fn "$1" solve); declare -F "$f" >/dev/null && "$f"; true; }
title_of(){ grep -m1 '^title:' "$(taskfile "$1")" | sed 's/^title:[[:space:]]*//'; }

cmd_start(){ do_setup "$1"; printf '%s▸ lab started:%s %s%s%s\n' "$G" "$R" "$B" "$(title_of "$1")" "$R"; printf '  reset to a clean slate. Do the work, then:  %slab grade %s%s\n' "$B" "$1" "$R"; }
cmd_grade(){ "$BIN" --task "$(taskfile "$1")"; }
cmd_solve(){ do_solve "$1"; printf '%sapplied the worked solution for %s. Grade with:  lab grade %s%s\n' "$Y" "$1" "$1" "$R"; }
cmd_finish(){ do_cleanup "$1"; printf '%s▸ lab %s reset to baseline.%s\n' "$G" "$1" "$R"; }
cmd_reset(){ local t; for t in "${TASKS[@]}"; do do_cleanup "$t"; done; printf '%s▸ all RHCSA tasks reset to baseline.%s\n' "$G" "$R"; }
cmd_list(){
  printf '%sRHCSA practice tasks%s\n' "$B" "$R"
  local t; for t in "${TASKS[@]}"; do printf '  %s%-13s%s %s%s%s\n' "$G" "$t" "$R" "$D" "$(title_of "$t")" "$R"; done
  printf '\nusage: %slab {start|grade|solve|finish} <task>%s   ·   %slab reset%s (all → baseline)\n' "$B" "$R" "$B" "$R"
}

verb=${1:-list}
case "$verb" in
  list|"") cmd_list ;;
  reset|reset-all) cmd_reset ;;
  start|grade|solve|finish)
    t=${2:-}; [ -n "$t" ] || die "usage: lab $verb <task>  (see: lab list)"
    t=${t#rhcsa-}; known "$t" || die "unknown task '$t' (see: lab list)"
    case "$verb" in
      start)  cmd_start  "$t" ;;
      grade)  cmd_grade  "$t" ;;
      solve)  cmd_solve  "$t" ;;
      finish) cmd_finish "$t" ;;
    esac ;;
  *) die "unknown command '$verb' (see: lab list)" ;;
esac
