#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
rules="${LAYERS_FILE:-layers.txt}"
module=$(go list -m)
root=$(go list -m -f '{{.Dir}}')
graph=$(mktemp)
trap 'rm -f "$graph"' EXIT
go list -f '{{.ImportPath}}|{{join .Imports " "}}|{{.Dir}}|{{join .GoFiles " "}}' ./... > "$graph"
awk -v module="$module" -v root="$root" -v rules="$rules" '
function clean(s) { sub(/[ \t]+#.*$/, "", s); return s }
FILENAME == rules {
  if ($0 ~ /^[ \t]*(#|$)/) next
  line = clean($0)
  if (index(line, " -> ") > 0) {
    split(line, p, " -> ")
    allow[p[1] SUBSEP p[2]] = 1
    next
  }
  pkg = line
  sub(/:.*$/, "", pkg)
  known[pkg] = 1
  rest = line
  sub(/^[^:]*:/, "", rest)
  n = split(rest, a, " ")
  for (i = 1; i <= n; i++) {
    allow[pkg SUBSEP a[i]] = 1
    if (a[i] == "database/sql") sqlok[pkg] = 1
  }
  next
}
{
  split($0, f, "|")
  pkg = f[1]
  sub("^" module "/?", "", pkg)
  if (pkg == "") pkg = "."
  seen++
  listed[pkg] = 1
  if (!(pkg in known)) {
    print pkg ": no entry in " rules
    bad = 1
    next
  }
  n = split(f[2], a, " ")
  for (i = 1; i <= n; i++) {
    imp = a[i]
    if (index(imp, module "/") == 1) imp = substr(imp, length(module) + 2)
    else if (imp != "database/sql") continue
    if ((pkg SUBSEP imp) in allow) used[pkg SUBSEP imp] = 1
    else {
      print pkg " → " imp ": not allowed by " rules
      bad = 1
    }
  }
  if (sqlok[pkg]) next
  dir = substr(f[3], length(root) + 2)
  if (dir != "") dir = dir "/"
  n = split(f[4], a, " ")
  for (i = 1; i <= n; i++) {
    path = dir a[i]
    hit = 0
    while ((getline text < (root "/" path)) > 0)
      if (text ~ /\.(QueryContext|ExecContext|QueryRowContext|BeginTx|Exec|Query|QueryRow)\(([^)]|$)/) hit = 1
    close(root "/" path)
    if (!hit) continue
    if ((path SUBSEP "sql") in allow) used[path SUBSEP "sql"] = 1
    else {
      print path ": SQL outside a package allowed database/sql"
      bad = 1
    }
  }
}
END {
  if (seen == 0) { print "no packages listed" > "/dev/stderr"; exit 1 }
  for (pkg in known)
    if (!(pkg in listed)) {
      print pkg ": listed in " rules " but not a package; remove it"
      bad = 1
    }
  for (k in allow) {
    split(k, p, SUBSEP)
    if (!(k in used) && (p[1] in listed || !(p[1] in known))) {
      print p[1] " → " p[2] ": allowed by " rules " but not used; remove it"
      bad = 1
    }
  }
  exit bad
}
' "$rules" "$graph"
