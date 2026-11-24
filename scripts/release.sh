#!/usr/bin/env bash
set -euo pipefail

cfg="$(cd "$(dirname "$0")/.." && pwd)/release.local"

die() { echo "$*" >&2; exit 1; }

setting() {
  local v
  v=$(grep "^$1=" "$cfg" | cut -d= -f2- || true)
  [ -n "$v" ] || die "release.local does not set $1; see release.local.example."
  printf '%s' "$v"
}

load() {
  [ -f "$cfg" ] || die "release.local is missing; copy release.local.example to release.local and fill it in."
  data=$(setting data_dir)
  rel=$(setting releases_dir)
  repo=$(setting repo_dir)
  [ -d "$data" ] || die "data_dir $data is not a folder."
  git -C "$repo" rev-parse --git-dir >/dev/null 2>&1 || die "repo_dir $repo is not a git checkout."
  port=$(sed -n 's/^port:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$data/sifty.yaml" 2>/dev/null || true)
  url=http://127.0.0.1:${port:-7373}
  mkdir -p "$rel"
}

holder() { lsof -t "$data/sifty.lock" 2>/dev/null || true; }

answers() { curl -fs --max-time 2 "$url/api/version" 2>/dev/null | grep -qF "$1"; }

wait_for() {
  local i
  for i in $(seq 180); do
    "$@" && return 0
    sleep 1
  done
  return 1
}

stop() {
  local pid i
  pid=$(holder)
  [ -n "$pid" ] || return 0
  kill "$pid"
  for i in $(seq 60); do
    [ -n "$(holder)" ] || return 0
    sleep 1
  done
  return 1
}

start() {
  ln -sfn "$rel/$1" "$rel/current"
  nohup "$rel/current/sifty" serve --data-dir "$data" </dev/null >>"$rel/serve.log" 2>&1 &
}

snapshot() { ls "$data/backups"/sifty-*.db 2>/dev/null || true; }

new_backup() { comm -13 <(printf '%s\n' "$before") <(snapshot) | tail -n 1; }

made_at() {
  local t=${1##*/sifty-}
  t=${t%%-v*}
  echo "${t:0:4}-${t:4:2}-${t:6:2} ${t:9:2}:${t:11:2}:${t:13:2} UTC"
}

restore() {
  rm -f "$data/sifty.db-wal" "$data/sifty.db-shm"
  cp "$1" "$data/sifty.db"
  echo "Restored the database copy $1, made at $(made_at "$1")."
}

entries() { awk '/^## /{u=/Unreleased/;next} u&&/^- /{n++} END{print n+0}' CHANGELOG.md; }

next_version() {
  local last a b c
  last=$(git tag --list 'v[0-9]*' | sort -V | tail -n 1)
  [ -n "$last" ] || { echo 0.1.0; return; }
  IFS=. read -r a b c <<<"${last#v}"
  if [ "$(entries)" -gt 0 ]; then echo "$a.$((b+1)).0"; else echo "$a.$b.$((c+1))"; fi
}

revert() {
  local made
  stop || die "Release $ver failed: the new build did not answer and still holds the data folder. Nothing was restored and nothing was started: the database is untouched and the application is down."
  made=$(new_backup)
  [ -z "$made" ] || restore "$made"
  rm -rf "$rel/$ver"
  if [ -z "$prev" ]; then
    rm -f "$rel/current"
    die "Release $ver failed: the new build did not answer, and no previous build is known; start the application by hand."
  fi
  start "$prev"
  wait_for answers "\"version\":\"$prev\"" || die "Release $ver failed: the new build did not answer, and the previous build $prev does not answer either."
  die "Release $ver failed: the new build did not answer; the previous build $prev runs again."
}

release() {
  load
  cd "$repo"
  [ -z "$(git status --porcelain)" ] || die "Release refused: the checkout has changes that are not committed."
  [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "Release refused: the checkout is not on the main branch."
  ver=$(next_version)
  [ ! -e "$rel/$ver" ] || die "Release refused: $rel/$ver exists; to finish that release run: bash scripts/release.sh publish $ver"
  mkdir -p "$rel/$ver"
  make build VERSION="$ver" BINARY="$rel/$ver/sifty" || { rm -rf "$rel/$ver"; die "Release refused: the build failed."; }
  sha=$(shasum -a 256 api/openapi.yaml | awk '{print $1}')
  prev=$(readlink "$rel/current" 2>/dev/null || true)
  prev=${prev##*/}
  while curl -fs --max-time 5 "$url/api/status" | grep -qF '"collecting":{'; do
    echo "A collection is in progress; waiting for it to finish."
    sleep 10
  done
  before=$(snapshot)
  stop || { rm -rf "$rel/$ver"; die "Release stopped: the running application did not exit."; }
  start "$ver"
  wait_for answers "\"version\":\"$ver\",\"api\":\"$sha\"" || revert
  made=$(new_backup)
  [ -z "$made" ] || echo "$made" > "$rel/$ver/restore"
  echo "$prev" > "$rel/$ver/from"
  echo "Release $ver is running."
  ls -d "$rel"/[0-9]* | sort -rV | tail -n +6 | while read -r d; do [ "${d##*/}" = "$prev" ] || rm -rf "$d"; done
  bash "${BASH_SOURCE[0]}" publish "$ver" || die "Release $ver is running, but publishing it failed; repeat with: bash scripts/release.sh publish $ver"
}

publish() {
  local tmp
  if ! grep -q "^## $1\$" CHANGELOG.md; then
    tmp=$(mktemp)
    awk -v v="$1" -v n="$(entries)" '/^## Unreleased/{print; print ""; print "## " v; if (n == 0) {print ""; print "No notable changes"}; next} {print}' CHANGELOG.md > "$tmp"
    cat "$tmp" > CHANGELOG.md
    rm -f "$tmp"
  fi
  git diff --quiet -- CHANGELOG.md || git commit -qm "chore(release): $1" CHANGELOG.md
  git rev-parse -q --verify "refs/tags/v$1" >/dev/null || git tag "v$1"
  git push -q origin main "v$1"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    publish) load; cd "$repo"; publish "${2:?usage: release.sh publish <version>}" ;;
    *) release ;;
  esac
fi
