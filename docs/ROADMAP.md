# Roadmap

The order in which Sifty is built. Each slice delivers behaviour the user can see working from end to end, described in `docs/behaviour/`, on the architecture in `docs/ARCHITECTURE.md`. A slice is accepted when its behaviour works on `make dev`, its tests pass, and the budgets still hold. A criterion that needs a job site is checked against recorded responses behind the fake `Fetcher`.

## Slices

1. **Skeleton.** `sifty serve` starts on an empty data folder and shows an empty Inbox with the hint to add sources. One binary with the frontend embedded; database with migrations and a backup before them; the one settings file with defaults; typed errors; `--offline`; `make build`, `make dev`, `make check`, and `make budget` measuring lines of code (later slices add the measures they introduce); `sifty version`. Accepted when a fresh folder starts without any setup and a second server on the same folder is refused.
2. **Collect and list.** Sources page with add, remove and enable; the "Collect jobs" button, automatic collection by interval, and progress in the header. Sources read through applicant tracking system boards and feeds; the one `Fetcher` with its safety and politeness rules; sanitised text; new, matched and merged vacancies. Inbox lists them with server-side search. Accepted when collection over recorded responses of the configured sources finds no false duplicates.
3. **Every source.** The generic source (JSON-LD, feeds, links) and the site-specific configurations; blocked domains remembered across restarts; per-source status, errors in words and "No vacancies in 30 days"; the one list of exclusions in Settings and "Block company".
4. **Decide.** The four lists with counters; Select, Dismiss, Undo, Move to Inbox, Apply with the confirmation dialog, Restore; the application as a separate fact; Delete with its rules; read marks; the keyboard; the vacancy page with its header and Listing text.
5. **Profiles and visibility.** My data and profiles with inheritance, the profile switcher, add, rename and delete; target roles, keywords and exclusions deciding what each profile sees.
6. **Fits you.** `Score` by code with the eight criteria, the Location rules and the countries-and-regions resolver; the Fits you band with its four tiles; buckets, the Fit filter and both sorts.
7. **Requirements.** The model foundation: providers, the per-kind choice in Settings, the dispatcher, the cost barrier and the call journal; background reading of vacancy texts; the requirements counter and the Requirements criterion; What they ask with + and −; What they offer. Accepted when a new vacancy text costs one model call and a change of profile costs none.
8. **Documents.** Files, pasted text and links; Analyze; suggestions with Accept, Reject and conflicts; "Use as evidence".
9. **Shortlist check.** The one model check when a vacancy enters Shortlist, shown next to the requirements code left undecided, run again only when its inputs change.
10. **Application.** The employer's page search and the Apply button states; form reading; answers from My data; label classification; drafting all answers in one call; answer cards, Write, Paste and manual questions.
11. **Keep and release.** Text retention after 60 days; `sifty backup`; the Background work page; the release command with its version, changelog and rollback. Switch-over from the previous version follows this slice.

## Status

Slices 1 to 9 are accepted: collection and the four lists, every kind of source, profiles and the visibility they decide, Fits you, the requirements read from a vacancy text, documents with their proposed facts, and the Shortlist check that stands beside the requirements the code left undecided.

Slice 10 is next. The frontend stands at 5,746 lines of 6,000 and slice 10 is estimated at about 800, so it does not fit. Either the profile editors give those lines back, as the Budgets section of `docs/ARCHITECTURE.md` says they should, or the budget rises; whichever happens is recorded in `docs/budget-log.md`.
