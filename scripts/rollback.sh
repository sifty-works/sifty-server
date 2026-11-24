#!/usr/bin/env bash
set -euo pipefail

. "$(dirname "$0")/release.sh"

rollback() {
  local cur prev copy
  load
  cur=$(readlink "$rel/current" 2>/dev/null || true)
  cur=${cur##*/}
  [ -n "$cur" ] || die "Rollback refused: no release is recorded in $rel."
  prev=$(cat "$rel/$cur/from" 2>/dev/null || true)
  [ -n "$prev" ] && [ -d "$rel/$prev" ] || die "Rollback refused: there is no previous build of $cur."
  copy=$(cat "$rel/$cur/restore" 2>/dev/null || true)
  if [ -n "$copy" ] && [ "${CONFIRM:-}" != 1 ]; then
    die "Rollback refused: release $cur changed the layout of the database, so going back restores the copy made at $(made_at "$copy") and discards everything entered since then. To restore it anyway run: make rollback CONFIRM=1"
  fi
  stop || die "Rollback stopped: the running application did not exit."
  [ -z "$copy" ] || restore "$copy"
  start "$prev"
  wait_for answers "\"version\":\"$prev\"" || die "Rollback failed: build $prev does not answer."
  echo "Rolled back from $cur to $prev."
}

rollback
