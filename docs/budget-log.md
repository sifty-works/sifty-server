# Budget log

Every change of a budget in the Budgets table of `docs/ARCHITECTURE.md`, newest first. An entry says when the budget changed, which measure, from what to what, what the lines are spent on, and why the change was accepted instead of making the code smaller. Lines are counted the way `scripts/budget.sh` counts them, so this log and the measure cannot drift apart. An entry is never edited once written; a change that is later undone gets an entry of its own.

## 2026-11-27 — test fakes and a development tool leave the backend count

The backend code measure stops counting `internal/models/fake.go` and `internal/fetch/fetchtest` (127 lines), which only tests use and which live outside `_test.go` files because tests of several packages share them, and `cmd/uncomment` (152 lines), a development tool that `layers.txt` already marks as outside the application. The budget stays 20,000 lines; the measure drops by 279. The owner decided it the same day, after a whole-system review found only about 35 lines that could go without changing behaviour: the budget limits the size of the application, and these lines are not part of it. The API generator in `cmd/sifty` stays counted, because it is built into the application.

## 2026-11-27 — budgets recorded late

The owner raised the frontend budget from 6,200 to 6,600 lines (goal lists per criterion) and the backend budget from 18,500 to 20,000 lines (vacancy analysis by code), both on 2026-11-27; `scripts/budget.sh` carried the new figures but this table and log did not, and are brought in line here.

## 2026-11-24 — the frontend gets room for the Monitor logs page

The frontend budget goes from 6,000 lines to 6,200, counted the way `scripts/budget.sh` counts. The 200 lines are spent on Settings › Monitor › Logs in the web client: the list of failed jobs and the list of the last warnings and errors of the log with their attributes, which `GET /api/logs` serves. The owner decided the rise the same day. It was accepted instead of making the client smaller because the page reads one new endpoint and shows two lists, and its lines are not taken from another capability; the estimate of the frontend (foundation 700 to 900) rises by the same 200, so the gap to the budget stays at about 450 lines, to be recovered in the profile editors. The backend budget is untouched.

## 2026-11-24 — the process grows to hold the release and rollback commands

The size of the process, which is `docs/PROCESS.md`, `AGENTS.md` and the scripts behind `make release`, `make rollback` and `make budget`, goes from 255 lines to 424 as `wc -l` counts them (from 190 to 337 counted the way `scripts/budget.sh` counts, without blank lines). `docs/PROCESS.md` and `AGENTS.md` keep their length; the 169 new lines are `scripts/release.sh` (144) and `scripts/rollback.sh` (25), which carry the release steps `docs/PROCESS.md` already described and that the coordinator did by hand: the refusals, the version, the build, the wait for collection, the switch with its way back and the restored database copy and its refusal to restore over a build that still holds the data folder, the changelog move, the tag and the five kept builds, and the return to the previous build, which asks for confirmation when it would discard data written since the release. The owner gave a standing permission the same day to raise any budget as long as this log records when, what, why and what the lines are spent on, and that permission covers this rise; the code budgets are untouched. The lines were accepted instead of a shorter command because the steps they hold are the ones the coordinator was performing by hand on the owner's live data folder, where a forgotten step costs a stopped application or a lost database.

## 2026-11-24 — the first line of the log

No budget has changed. The table stands as it was first written: backend code at most 18,500 lines, frontend code at most 6,000, and beside them the measures of data size, model calls per kind of work, and list speed. Measured on this day: backend code 15,271 lines, frontend code 5,746.
