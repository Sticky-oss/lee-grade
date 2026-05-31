#!/usr/bin/env bash
# recon — CALYX intel. Capabilities and systems status for the operative.
# Installed on the VM as /usr/local/bin/recon.
set -uo pipefail
BIN=/home/lee/payload/lee-grade
C=$'\e[96m'; G=$'\e[92m'; Y=$'\e[93m'; M=$'\e[1;95m'; D=$'\e[2m'; B=$'\e[1m'; R=$'\e[0m'

ntypes=$("$BIN" --list-check-types 2>/dev/null | wc -l)
printf '\n  %s░ CALYX INTEL ░%s  %s%s%s\n' "$M" "$R" "$D" "$("$BIN" --version 2>/dev/null)" "$R"
printf '  %s────────────────────────────────────────────────────────────%s\n' "$C" "$R"
printf '  %sINSTRUMENTS%s  %s%s graded check types I can run against a host%s\n' "$Y" "$R" "$D" "$ntypes" "$R"
"$BIN" --list-check-types 2>/dev/null | sort | tr '\n' ' ' | fold -s -w 60 | sed 's/^/    /'
printf '\n\n  %sTRAINING TRACKS%s\n' "$Y" "$R"
printf '    RHCSA (EX200)   12 directives    %sexam rhcsa%s · %sexam rhcsa-full%s · %slab list%s\n' "$G" "$R" "$G" "$R" "$G" "$R"
printf '    RHCE  (EX294)    ansible track    %sexam rhce%s\n' "$G" "$R"
printf '\n  %sFIELD CLUSTER%s\n' "$Y" "$R"
if sudo podman ps --format '{{.Names}}' 2>/dev/null | grep -q '^node'; then
  sudo podman ps --format "    {{.Names}}  {{.Status}}" 2>/dev/null
  printf '    grade the cluster: %sfleet%s\n' "$G" "$R"
else
  printf '    %sno managed nodes online%s — provision: labs/multinode-setup.sh\n' "$D" "$R"
fi
printf '\n  %snode%s %s\n\n' "$D" "$R" "$(uptime -p 2>/dev/null || true)"
