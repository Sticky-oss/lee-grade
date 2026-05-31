#!/usr/bin/env bash
# exam — CALYX certification interface. Thin, friendly wrapper over
# `lee-grade exam` so an operative never types a full exam path.
# Installed on the VM as /usr/local/bin/exam.
set -uo pipefail
BIN=/home/lee/payload/lee-grade
EXDIR=/home/lee/exams
G=$'\e[92m'; B=$'\e[1m'; R=$'\e[0m'

# Grading reads privileged host state (crontab -u, firewall, semanage).
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi

resolve(){ case "$1" in
  rhcsa|rhcsa-sample)                 echo "$EXDIR/rhcsa-9-sample.yaml" ;;
  rhcsa-full|rhcsa-practice|practice) echo "$EXDIR/rhcsa-9-practice.yaml" ;;
  rhce|rhce-sample)                   echo "$EXDIR/rhce-9-sample.yaml" ;;
  *)                                  echo "" ;;
esac; }

usage(){
  printf '%sCALYX certification%s\n' "$B" "$R"
  printf '  %sexam rhcsa%s        sit the RHCSA sample (6 directives)\n' "$G" "$R"
  printf '  %sexam rhcsa-full%s   sit the full RHCSA practice (12 directives)\n' "$G" "$R"
  printf '  %sexam rhce%s         sit the RHCE certification\n' "$G" "$R"
  printf '  %sexam status%s       time remaining + objectives\n' "$G" "$R"
  printf '  %sexam grade%s        submit for scoring\n' "$G" "$R"
  printf '  %sexam reset%s        abandon the current sitting\n' "$G" "$R"
}

start_exam(){ local f; f=$(resolve "$1"); [ -n "$f" ] || { echo "exam: unknown exam '$1' (try: rhcsa, rhcsa-full, rhce)"; exit 2; }; "$BIN" exam reset >/dev/null 2>&1; "$BIN" exam start "$f"; }

verb=${1:-status}
case "$verb" in
  ""|status)        "$BIN" exam status ;;
  grade)            "$BIN" exam grade ;;
  reset)            "$BIN" exam reset ;;
  list|help|-h|--help) usage ;;
  start)            start_exam "${2:-}" ;;
  *)                start_exam "$verb" ;;
esac
