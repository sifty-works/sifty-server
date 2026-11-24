# Budget log

Every change of a budget in the Budgets table of `docs/ARCHITECTURE.md`, newest first. An entry says when the budget changed, which measure, from what to what, what the lines are spent on, and why the change was accepted instead of making the code smaller. Lines are counted the way `scripts/budget.sh` counts them, so this log and the measure cannot drift apart. An entry is never edited once written; a change that is later undone gets an entry of its own.

## 2026-11-24 — the first line of the log

No budget has changed. The table stands as it was first written: backend code at most 18,500 lines, frontend code at most 6,000, and beside them the measures of data size, model calls per kind of work, and list speed. Measured on this day: backend code 15,271 lines, frontend code 5,746.
