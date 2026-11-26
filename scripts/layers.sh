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
    if ($0 !~ /^never / && $0 !~ /# until: [^ ]/) {
      print rules ": exception without \"# until: <change>\": " $0
      bad = 1
    }
    if (p[1] ~ /^never /) never[substr(p[1], 7) SUBSEP p[2]] = 1
    else allow[p[1] SUBSEP p[2]] = 1
    next
  }
  head = line
  sub(/:.*$/, "", head)
  rest = line
  sub(/^[^:]*:/, "", rest)
  n = split(head, h, " ")
  kind = h[1]
  owner = h[2]
  m = split(rest, a, " ")
  for (i = 1; i <= m; i++) {
    if (kind == "worker") { worker[a[i]] = 1; continue }
    if (kind == "sql") { sqlok[a[i]] = 1; continue }
    if (known[a[i]]) { print rules ": " a[i] " is listed twice"; bad = 1 }
    known[a[i]] = 1
    class[a[i]] = kind
    modof[a[i]] = owner
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
  dir = substr(f[3], length(root) + 2)
  if (dir != "") dir = dir "/"
  nf = split(f[4], files, " ")
  store = 0
  for (i = 1; i <= nf; i++) if (files[i] ~ /^(repo|store)[^\/]*\.go$/) store = 1
  n = split(f[2], a, " ")
  for (i = 1; i <= n; i++) {
    imp = a[i]
    if (index(imp, module "/") == 1) imp = substr(imp, length(module) + 2)
    else if (imp != "database/sql") continue
    if ((pkg SUBSEP imp) in never) {
      print pkg " → " imp ": never allowed by " rules
      bad = 1
      continue
    }
    c = class[pkg]
    t = class[imp]
    why = ""
    if (imp == "database/sql") {
      if (!sqlok[pkg] && !store) why = "database/sql outside a store file or a package listed after sql"
    } else if (!(imp in known)) {
      why = imp " has no entry in " rules
    } else if (c == "outside" || c == "wiring" || t == "mechanism" && (c == "mechanism" || c == "module")) {
    } else if (c == "module" && t == "module" && modof[pkg] == modof[imp]) {
      if (worker[pkg] && worker[imp]) why = "a worker imports another worker"
    } else if (c == "mechanism") {
      why = "a mechanism imports a " t " (" modof[imp] ")"
    } else {
      why = "module " modof[pkg] " imports " (t == "module" ? "module " modof[imp] : t)
    }
    if (why == "") continue
    if ((pkg SUBSEP imp) in allow) used[pkg SUBSEP imp] = 1
    else {
      print pkg " → " imp ": " why
      bad = 1
    }
  }
  if (sqlok[pkg]) next
  n = split(f[4], a, " ")
  for (i = 1; i <= n; i++) {
    path = dir a[i]
    hit = 0
    while ((getline text < (root "/" path)) > 0)
      if (text ~ /\.(QueryContext|ExecContext|QueryRowContext|BeginTx|Exec|Query|QueryRow)\(([^)]|$)/) hit = 1
    close(root "/" path)
    if (!hit) continue
    if (a[i] ~ /^(repo|store)/) continue
    if ((path SUBSEP "sql") in allow) used[path SUBSEP "sql"] = 1
    else {
      print path ": SQL outside a store file"
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
    if (!(k in used)) {
      print p[1] " → " p[2] ": allowed by " rules " but not used; remove it"
      bad = 1
    }
  }
  exit bad
}
' "$rules" "$graph"
