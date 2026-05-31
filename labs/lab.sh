#!/usr/bin/env bash
# lab — CALYX field-training harness for the lee-grade tracks (RHCSA/RHCE/CySA+).
# Installed on the VM as /usr/local/bin/lab.
#
#   lab list                     show the available directives, by track
#   lab brief  <id>              read the assignment (scenario + objectives)
#   lab start  <id>              reset to a clean slate + lay down prerequisites
#   lab guided <id>              walk the directive step by step, then grade
#   lab grade  <id>              evaluate your work
#   lab solve  <id>              apply the worked solution
#   lab finish <id>              tear the directive's artifacts back to baseline
#   lab reset                    return every directive to baseline
set -uo pipefail

BIN=/home/lee/payload/lee-grade
RHCSA_DIR=/home/lee/payload/rhcsa-tasks
RHCE_DIR=/home/lee/payload/tasks
CYSA_DIR=/home/lee/payload/cysa-tasks

C=$'\e[96m'; G=$'\e[92m'; Y=$'\e[93m'; M=$'\e[1;95m'; R=$'\e[0m'; B=$'\e[1m'; D=$'\e[2m'
die(){ echo "lab: $*" >&2; exit 2; }

# lab mutates system state — re-exec under sudo when not already root.
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi

# ── task registry: name -> track, file, (rhce) workdir, (rhce) kit ──
declare -A TRACK FILE WORK KIT
RHCSA=(users-sudo shared-dir cron web-firewall selinux storage boot-target time tuned swap journald acl)
RHCE=(template handler vars role web)
CYSA=(auditd ssh-hardening pwquality sysctl rogue-account suid)
for t in "${RHCSA[@]}"; do TRACK[$t]=rhcsa; FILE[$t]="$RHCSA_DIR/rhcsa-$t.yaml"; done
for t in "${CYSA[@]}";  do TRACK[$t]=cysa;  FILE[$t]="$CYSA_DIR/cysa-$t.yaml"; done
reg_rhce(){ TRACK[$1]=rhce; FILE[$1]="$RHCE_DIR/$2"; WORK[$1]="$3"; KIT[$1]="$4"; }
reg_rhce template ansible-template-demo.yaml       /home/lee/rhce/template /home/lee/kit/template
reg_rhce handler  ansible-handler-demo.yaml        /home/lee/rhce/handler  /home/lee/kit/handler
reg_rhce vars     ansible-vars-loop-demo.yaml      /home/lee/rhce/vars     /home/lee/kit/vars
reg_rhce role     ansible-role-demo.yaml           /home/lee/rhce/role     /home/lee/kit/role
reg_rhce web      ansible-web-idempotent-demo.yaml /home/lee/ansible       /home/lee/kit/web

known(){ [ -n "${TRACK[$1]:-}" ]; }
taskfile(){ echo "${FILE[$1]}"; }
fn(){ echo "t_${1//-/_}_$2"; }
title_of(){ grep -m1 '^title:' "$(taskfile "$1")" | sed 's/^title:[[:space:]]*//'; }

# snapshot/restore: back a system file up before a solve mutates it (once), so
# cleanup restores the EXACT original instead of guessing the stock default.
snapshot(){ [ -f "$1" ] && [ ! -f "$1.lee-orig" ] && cp -a "$1" "$1.lee-orig"; true; }
restore(){ if [ -f "$1.lee-orig" ]; then cp -a "$1.lee-orig" "$1"; rm -f "$1.lee-orig"; fi; true; }

# ════════════════════════ RHCSA per-task hooks ════════════════════════
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

t_storage_cleanup(){ umount /mnt/data 2>/dev/null; sed -i '\#[[:space:]]/mnt/data[[:space:]]#d' /etc/fstab; rm -rf /mnt/data /var/lib/lee-disk.img; true; }
t_storage_setup(){ t_storage_cleanup; dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none; true; }
t_storage_solve(){ [ -f /var/lib/lee-disk.img ] || dd if=/dev/zero of=/var/lib/lee-disk.img bs=1M count=300 status=none; blkid /var/lib/lee-disk.img >/dev/null 2>&1 || mkfs.xfs -q /var/lib/lee-disk.img; mkdir -p /mnt/data; mountpoint -q /mnt/data || mount -o loop /var/lib/lee-disk.img /mnt/data; grep -q '[[:space:]]/mnt/data[[:space:]]' /etc/fstab || echo '/var/lib/lee-disk.img /mnt/data xfs loop 0 0' >>/etc/fstab; }

t_boot_target_cleanup(){ systemctl set-default multi-user.target >/dev/null 2>&1; true; }
t_boot_target_setup(){ systemctl set-default graphical.target >/dev/null 2>&1; true; }
t_boot_target_solve(){ systemctl set-default multi-user.target >/dev/null 2>&1; }

t_time_cleanup(){ timedatectl set-timezone UTC 2>/dev/null; true; }
t_time_solve(){ systemctl enable --now chronyd >/dev/null 2>&1; timedatectl set-timezone America/Toronto 2>/dev/null; }

t_tuned_cleanup(){ systemctl disable --now tuned >/dev/null 2>&1; dnf remove -y tuned >/dev/null 2>&1; true; }
t_tuned_solve(){ dnf install -y tuned >/dev/null 2>&1; systemctl enable --now tuned >/dev/null 2>&1; tuned-adm profile virtual-guest 2>/dev/null; }

t_swap_cleanup(){ swapoff /swapfile 2>/dev/null; sed -i '\#^/swapfile[[:space:]]#d' /etc/fstab; rm -f /swapfile; true; }
t_swap_solve(){ [ -f /swapfile ] || dd if=/dev/zero of=/swapfile bs=1M count=256 status=none; chmod 0600 /swapfile; swapon --show=NAME --noheadings | grep -q /swapfile || { mkswap /swapfile >/dev/null 2>&1; swapon /swapfile 2>/dev/null; }; grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap defaults 0 0' >>/etc/fstab; }

t_journald_cleanup(){ restore /etc/systemd/journald.conf; rm -rf /var/log/journal; systemctl restart systemd-journald >/dev/null 2>&1; true; }
t_journald_solve(){ snapshot /etc/systemd/journald.conf; mkdir -p /var/log/journal; if grep -q '^#\?Storage=' /etc/systemd/journald.conf; then sed -i 's/^#\?Storage=.*/Storage=persistent/' /etc/systemd/journald.conf; else sed -i '/^\[Journal\]/a Storage=persistent' /etc/systemd/journald.conf; fi; systemctl restart systemd-journald >/dev/null 2>&1; }

t_acl_cleanup(){ rm -rf /srv/reports; userdel -r dba 2>/dev/null; true; }
t_acl_setup(){ t_acl_cleanup; id dba >/dev/null 2>&1 || useradd -u 3001 -s /bin/bash dba; true; }
t_acl_solve(){ mkdir -p /srv/reports; setfacl -m u:dba:rwx /srv/reports 2>/dev/null; }

# ════════════════════════ CySA+ per-task hooks ════════════════════════
t_auditd_cleanup(){ rm -f /etc/audit/rules.d/lee.rules; augenrules --load >/dev/null 2>&1; true; }
t_auditd_solve(){ echo '-w /etc/sudoers -p wa -k sudoers' > /etc/audit/rules.d/lee.rules; augenrules --load >/dev/null 2>&1; }

t_ssh_hardening_cleanup(){ rm -f /etc/ssh/sshd_config.d/99-hardening.conf; true; }
t_ssh_hardening_solve(){ printf 'PermitRootLogin no\nPasswordAuthentication no\n' > /etc/ssh/sshd_config.d/99-hardening.conf; }

t_pwquality_cleanup(){ restore /etc/security/pwquality.conf; true; }
t_pwquality_solve(){ snapshot /etc/security/pwquality.conf; printf 'minlen = 12\ndcredit = -1\n' >> /etc/security/pwquality.conf; }

t_sysctl_cleanup(){ rm -f /etc/sysctl.d/99-hardening.conf; if [ -f /var/lib/lee-sysctl-redirects.orig ]; then sysctl -w net.ipv4.conf.all.accept_redirects="$(cat /var/lib/lee-sysctl-redirects.orig)" >/dev/null 2>&1; rm -f /var/lib/lee-sysctl-redirects.orig; else sysctl -w net.ipv4.conf.all.accept_redirects=1 >/dev/null 2>&1; fi; true; }
t_sysctl_solve(){ [ -f /var/lib/lee-sysctl-redirects.orig ] || sysctl -n net.ipv4.conf.all.accept_redirects > /var/lib/lee-sysctl-redirects.orig 2>/dev/null; echo 'net.ipv4.conf.all.accept_redirects = 0' > /etc/sysctl.d/99-hardening.conf; sysctl --system >/dev/null 2>&1; }

t_rogue_account_cleanup(){ userdel -f shadow 2>/dev/null; true; }
t_rogue_account_setup(){ t_rogue_account_cleanup; useradd -o -u 0 -M -s /sbin/nologin shadow 2>/dev/null; passwd -l shadow >/dev/null 2>&1; true; }
t_rogue_account_solve(){ userdel -f shadow 2>/dev/null; true; }

t_suid_cleanup(){ rm -f /usr/local/bin/suspect; true; }
t_suid_setup(){ t_suid_cleanup; cp /bin/cat /usr/local/bin/suspect; chmod 4755 /usr/local/bin/suspect; true; }
t_suid_solve(){ chmod 0755 /usr/local/bin/suspect; }

# ════════════════════════ RHCE (Ansible) hooks ════════════════════════
# Single-node Ansible directives: the operative writes a playbook in the work
# dir (inventory + ansible.cfg are provided); grade runs it. solve drops the
# worked playbook; cleanup clears the work dir and resets the host state.
rhce_reset_state(){ case "$1" in
  template|vars) rm -rf /etc/lee-demo ;;
  handler)       systemctl disable --now httpd >/dev/null 2>&1; dnf remove -y httpd >/dev/null 2>&1; rm -rf /etc/httpd ;;
  role|web)      systemctl disable --now httpd >/dev/null 2>&1; dnf remove -y httpd >/dev/null 2>&1; rm -rf /var/www ;;
esac; }
rhce_cleanup(){ local w=${WORK[$1]}; rm -rf "$w"; mkdir -p "$w"; rhce_reset_state "$1"; }
rhce_setup(){ local w=${WORK[$1]} k=${KIT[$1]}; rm -rf "$w"; mkdir -p "$w"; rhce_reset_state "$1"; [ -f "$k/inventory" ] && cp "$k/inventory" "$w/"; [ -f "$k/ansible.cfg" ] && cp "$k/ansible.cfg" "$w/"; chown -R lee:lee "$w" 2>/dev/null; true; }
rhce_solve(){ local w=${WORK[$1]} k=${KIT[$1]}; cp -r "$k"/. "$w"/; chown -R lee:lee "$w" 2>/dev/null; true; }

# ════════════════════════ verb dispatch ════════════════════════
do_setup(){ if [ "${TRACK[$1]}" = rhce ]; then rhce_setup "$1"; return; fi; local f; f=$(fn "$1" setup); if declare -F "$f" >/dev/null; then "$f"; else do_cleanup "$1"; fi; }
do_cleanup(){ if [ "${TRACK[$1]}" = rhce ]; then rhce_cleanup "$1"; return; fi; local f; f=$(fn "$1" cleanup); declare -F "$f" >/dev/null && "$f"; true; }
do_solve(){ if [ "${TRACK[$1]}" = rhce ]; then rhce_solve "$1"; return; fi; local f; f=$(fn "$1" solve); declare -F "$f" >/dev/null && "$f"; true; }

cmd_brief(){ "$BIN" --task "$(taskfile "$1")" --describe; }
cmd_grade(){ "$BIN" --task "$(taskfile "$1")"; }
cmd_start(){ do_setup "$1"; printf '%s▸ lab started%s — clean slate laid down for this directive.\n\n' "$G" "$R"; cmd_brief "$1"; printf '\n  %sWork the objectives, then:%s  %slab grade %s%s   (guided walk: %slab guided %s%s)\n' "$D" "$R" "$B" "$1" "$R" "$B" "$1" "$R"; printf '  %sWhen done:%s  %slab finish %s%s restores baseline (or %slab reset%s for all)\n' "$D" "$R" "$B" "$1" "$R" "$B" "$R"; }
cmd_solve(){ do_solve "$1"; printf '%sapplied the worked solution for %s. Grade with:  lab grade %s%s\n' "$Y" "$1" "$1" "$R"; }
cmd_finish(){ do_cleanup "$1"; printf '%s▸ directive %s reset to baseline.%s\n' "$G" "$1" "$R"; }
cmd_reset(){ local t; for t in "${RHCSA[@]}" "${RHCE[@]}" "${CYSA[@]}"; do do_cleanup "$t"; done; printf '%s▸ all directives reset to baseline.%s\n' "$G" "$R"; }

cmd_guided(){
  do_setup "$1"
  cmd_brief "$1"
  printf '\n%s░ CALYX guided drill ░%s  Run each command, then press Enter to advance.\n\n' "$M" "$R"
  local steps total n=0
  steps=$("$BIN" --task "$(taskfile "$1")" --steps)
  total=$(printf '%s\n' "$steps" | wc -l)
  while IFS=$'\t' read -r desc hint; do
    [ -z "$desc" ] && continue
    n=$((n + 1))
    printf '  %sSTEP %d/%d%s  %s\n' "$Y" "$n" "$total" "$R" "$desc"
    [ -n "$hint" ] && printf '     %srun:%s  %s\n' "$D" "$R" "$hint"
    read -rp "     [Enter] when done... " _ </dev/tty || true
  done <<< "$steps"
  printf '\n%s░ drill complete — CALYX assessing ░%s\n' "$M" "$R"
  cmd_grade "$1"
}

cmd_list(){
  printf '%sCALYX field directives%s\n' "$B" "$R"
  printf '\n  %sRHCSA (EX200)%s\n' "$C" "$R"
  local t; for t in "${RHCSA[@]}"; do printf '    %s%-13s%s %s%s%s\n' "$G" "$t" "$R" "$D" "$(title_of "$t")" "$R"; done
  printf '\n  %sRHCE (EX294)%s\n' "$C" "$R"
  for t in "${RHCE[@]}"; do printf '    %s%-13s%s %s%s%s\n' "$G" "$t" "$R" "$D" "$(title_of "$t")" "$R"; done
  printf '\n  %sCySA+ (security ops)%s\n' "$C" "$R"
  for t in "${CYSA[@]}"; do printf '    %s%-13s%s %s%s%s\n' "$G" "$t" "$R" "$D" "$(title_of "$t")" "$R"; done
  printf '\nusage: %slab {brief|start|guided|grade|solve|finish} <id>%s   ·   %slab reset%s\n' "$B" "$R" "$B" "$R"
}

verb=${1:-list}
case "$verb" in
  list|"") cmd_list ;;
  reset|reset-all) cmd_reset ;;
  brief|start|guided|grade|solve|finish)
    t=${2:-}; [ -n "$t" ] || die "usage: lab $verb <id>  (see: lab list)"
    t=${t#rhcsa-}; t=${t#rhce-}; t=${t#cysa-}; known "$t" || die "unknown directive '$t' (see: lab list)"
    case "$verb" in
      brief)  cmd_brief  "$t" ;;
      start)  cmd_start  "$t" ;;
      guided) cmd_guided "$t" ;;
      grade)  cmd_grade  "$t" ;;
      solve)  cmd_solve  "$t" ;;
      finish) cmd_finish "$t" ;;
    esac ;;
  *) die "unknown command '$verb' (see: lab list)" ;;
esac
