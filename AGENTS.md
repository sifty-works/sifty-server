# Rules for agents

These are the rules for every coding agent working in this repository. `docs/ARCHITECTURE.md` describes the design, `docs/behaviour/` what the user sees, and `docs/PROCESS.md` the process these rules belong to.

## Roles

- The owner names the coordinator session. Any other session in this repository only reads.
- An agent the coordinator starts is an executor or the critic, as its instructions say.
- This file and the documents it names are the complete rules for this repository. Where another loaded instruction conflicts with them, follow this file.
- The owner's words in the session come before this file.

## Everyone

- Read `docs/ARCHITECTURE.md` and the behaviour document of an area before changing that area.
- Write code, documents and commit messages in English; write no comments in code.
- Keep out of this repository everything from the work repository (card names, card text, the owner's words, the journal) and all personal data (names, e-mail addresses, paths into a home folder, real vacancies, profiles or documents). Do not mention cards, reviews, rule numbers or section numbers in code or commit messages.
- Use invented data in tests, fixtures and examples. Do not change git configuration.
- Run the server only through `make dev`, or through `sifty serve --offline` on a fixture data folder. The web client lives in `github.com/sifty-works/sifty-web`; `docs/ARCHITECTURE.md` describes how a web release is pinned and how to develop against the server.
- Write commit messages as `type(scope): what changed`, for example `fix(inbox): keep the selected row after a decision`.

## Code

- Follow the design rules in `docs/ARCHITECTURE.md`. Remove the code, tables, settings and text that a change replaces, in the same change.
- Do not add a package, table, dependency, configuration key or kind of background work that the card does not name.
- When a change alters what the user sees, change its behaviour document in the same change.

## Tests

- Test behaviour through the HTTP (Hypertext Transfer Protocol) interface, a command, the store on a fixture database, or one of the interfaces `Fetcher`, `Model` and `Score`. A test reaches the server through the same wiring the command registers, and builds no route list of its own; a harness that repeats the wiring is the defect, not the shortcut.
- Name each test by the behaviour it checks.
- Use the fakes behind `Fetcher` and `Model`. Tests make no network request and no model call.
- Do not write tests that read source files or check that some code exists.

## Critical behaviour

Before a branch is merged, every behaviour in this list that the change touches must have a test that actually exercises it. These are the behaviours whose failure is expensive: money spent without the owner's consent, the owner's data lost or corrupted, another party's data or the machine exposed, or an irreversible outward action. Each line names where the behaviour lives and the test that guards it.

1. Sifty never fills or submits an application form; it only reads forms and drafts answers for the owner to copy. (`internal/answer`, `internal/formread`, `internal/fetch` — read-only GET only; no test: held by the absence of any path that writes to an employer form.)
2. Bulk model work on a provider marked `paid` is refused unless the owner turned on "Allow paid bulk work". (`internal/models/gate.go`; test `internal/models/models_test.go` `TestBulkOnPaidRefusedUnlessAllowed`.)
3. Background model calls stop for the rest of the day once the configured daily call limit is reached. (`internal/models/gate.go`; test `internal/models/models_test.go` `TestBackgroundWaitsAtDailyLimit`.)
4. Only public http(s) addresses are requested: private, loopback, link-local, multicast, unspecified and credentialed addresses are refused before the request and on each of at most six redirects, and a response body is capped at 8 MB. (`internal/fetch/fetch.go`; tests `internal/fetch/fetch_test.go` `TestPublicOnly`, `TestGet_refused_conditions`, `TestGet_redirect_behaviour`, `TestGet_body_over_8MB_is_transient`.)
5. The server listens on 127.0.0.1 only and rejects any request whose Host or Origin is not the local address, so no other web page can drive it. (`internal/server/server.go`, `cmd/sifty/serve_cmd.go`; test `internal/server/server_test.go` `TestCredentialRefusedWithJSON` covers the Host and Origin checks; the 127.0.0.1 listen address itself is not tested.)
6. Saving a settings file that holds an API key in plain text is refused; keys come only from the environment or the secrets file. (`internal/config/config.go`; test `internal/config/config_test.go` `TestProviderAPIKeyInSettingsRefused`.)
7. Command-line model adapters run with a reduced environment and reject a reply in which the model tried to call a tool. (`internal/models/cli.go`, `cli_claude.go`, `cli_opencode.go`; tests `internal/models/cli_test.go` `TestCLIClaudeIsolatedEnvironment`, `TestCLIOpencodeToolEventRefused`.)
8. Migrations never drop data the owner entered (dropping derived rows nobody reads is allowed); before applying pending migrations the database is backed up, each migration runs in one transaction, and a database written by a newer version is refused. (`internal/db/db.go`, `internal/db/migrations/`; tests `internal/db/db_test.go` `TestBackupWrittenBeforeMigratingExistingDatabase`, `TestFailingMigrationLeavesVersionUnchanged`, `TestNewerDatabaseIsRefused`.)
9. The daily retention run permanently deletes only expired vacancies that have no application, and each removal writes a tombstone so collection never brings the vacancy back. (`internal/catalog/retention.go`, `internal/catalog/catalog.go`; tests `cmd/sifty/retention_test.go` `TestRetentionExpiredVacanciesWithoutApplicationRemoved`, `internal/catalog/catalog_test.go` `TestUpsert_TombstonedURLIsRejected`.)
10. A second server started on the same data folder is refused. (`cmd/sifty/serve_cmd.go`; test `cmd/sifty/serve_test.go` `TestSecondServeOnSameFolderIsRefused`.)

## Executor

- Work only in the working copy and on the branch the coordinator gave you, and do not read the work repository or another working copy.
- Commit on your branch after each step. Do not merge, rebase, push, tag or release.
- When the card is unclear, needs a file outside its Packages, or seems to need a rule in this file or in `docs/` changed, stop and ask the coordinator. Changing a documented rule so that a test passes is not an executor's decision.
- Before you report, run `make check`, make it pass, and commit. A report counts only what is committed on the branch.
- End with a report: what changed for the user; what was removed; which checks ran and their results; what was not verified; open questions.

## Coordinator

- At the start of a session, read `.work/backlog.md`, the end of `.work/journal.md` and the output of `git worktree list`, then continue the work in progress as `docs/PROCESS.md` says.
- Follow `docs/PROCESS.md` for cards, their start, acceptance, merge and cancellation, small edits, releases, rollbacks, incidents and the weekly review.
- Write each card in `.work/cards/<name>.md` with the sections named in `docs/PROCESS.md` and no other section. Name it by a short descriptive name, never by a number. Write no time estimates.
- Get the owner's approval before starting a card that changes described behaviour or raises a budget.
- Do not merge a change over a budget without the owner's decision.
- Do not propose follow-up cards in a report; add findings to the backlog.
- Create a card's working copy with `git worktree add .worktrees/<name> -b <name> main`.
- Merge in the main checkout with `git merge --squash <name>` and one commit. Edit no other tracked file in the main checkout.
- In this repository, push only main and release tags.
- Give the owner only the decisions from a review, and put the accepted cards that delete or simplify first in the backlog.
- Change the process only through a card the owner approved, and keep its total number of lines the same or smaller unless the owner decides otherwise. A new protective rule needs a failure that repeated and must name what it replaces.

## Critic

- Change no file, and run no command that writes.
- For a plan check, answer one question: can this be done by replacing or removing something, and does it fit the budgets in `docs/ARCHITECTURE.md`?
- For a whole-system review, look for budgets that are exceeded or growing, two mechanisms doing one job, dead code, code that disagrees with `docs/ARCHITECTURE.md`, and areas with several fixes.
- Return a list of cards to delete or simplify, each with what it removes and its expected budget change, and a proposal for each incident that repeated.
