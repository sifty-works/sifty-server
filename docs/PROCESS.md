# Development process

This document says how Sifty is developed: who does what, how a piece of work goes from an idea to a release, and how the process itself changes.

## Roles

- The **owner** is the person Sifty is built for. The owner decides what is built, approves every change of described behaviour and of budgets before the card starts, sets the thresholds of the measurement, accepts the demo that closes an epic, looks at a preview when a card asks for one, and takes the decisions of each weekly review.
- The **coordinator** is the one agent session that writes to this repository. The owner names it. It writes cards, runs executors, accepts and merges their work, releases, and keeps the work repository. The coordinator decides operational matters alone and does not ask the owner about them: accepting a card by its Checks and the acceptance steps (a card without criteria is a process defect that the coordinator fixes), releasing, and taking the next card from the current epic until it is empty. The owner is asked only about what the owner needs, and that is settled before a card starts, in the spec of new functionality. Any other session in this repository only reads.
- An **executor** is an agent that the coordinator starts for one card. It works only in its own working copy, on its own branch.
- The **critic** is an agent that reads and changes nothing. It checks the plan of the cards that need it, reviews the code of each branch at acceptance and measures its budgets, and reviews the whole system every week.
- The **test reviewer** and the **process reviewer** are agents that read and change nothing. The test reviewer reviews the Tests section of a card before work and the test hunks that the acceptance script routes to it. The process reviewer measures the process at the weekly review.

Four more words are used below. A **card** is the description of one piece of work, from its reason to its report. An **epic** is a goal that several cards serve, with a spec that says what "done" means. A **working copy** is a git worktree: a separate folder on its own branch that shares the repository's history. The **work repository** is a private repository that holds the cards, the backlog, the handoff note, the journal and the scripts of the work process.

## In brief

The process rests on six arrangements. Each guarantee is held by one of them, not by checks added on top.

1. **One writer.** Only the coordinator writes to the main branch, and only by merging an accepted card or by releasing. Executors work in parallel, each on its own card, branch and working copy. Because no two writes to the main branch can happen at once, the process needs no locks and no hooks.
2. **Merging is not releasing.** A merge changes the main branch only. The running application changes only through `make release`, which gives the release a version and changelog entries. `make rollback` undoes a release in one command.
3. **Every card names what it replaces.** A card says what it replaces and removes and how it changes the budgets. The critic measures the budgets on each branch, and going over a budget is the owner's decision.
4. **Work comes from the current epic, inside a skeleton, with tests declared before work.** An epic closes only on a demo with the measurement next to it. A script compares the declared tests with the branch at acceptance.
5. **Findings are decided, not stored.** The weekly review measures the process. An incident is recorded and looked at with the others there, not patched at once. A new protective rule needs a failure that repeated, and the process grows in size only by the owner's decision.
6. **Code is public and work is private.** This repository holds code and documents only. The cards, the owner's words and the journal live in the work repository.

## Where things live

- This repository (`sifty-works/sifty-server`) is public. It holds the Go server, the tests, `README.md`, `CHANGELOG.md`, `AGENTS.md`, `CLAUDE.md` and `docs/`. `CLAUDE.md` holds one line, `@AGENTS.md`, which makes Claude Code load `AGENTS.md`. The web client lives in `sifty-works/sifty-web`; a versioned release archive is pinned in `web/release.txt` and embedded into the binary by `make build`.
- Nothing from the work repository enters this repository. Code, tests, commit messages and changelog entries describe a change in its own terms and never name a card. Test data is invented.
- The work repository is checked out into `.work/` of the folder that holds this checkout, so it stands beside the application repositories rather than inside one of them, and this repository ignores a `.work/` of its own. It holds `handoff.md`, which opens with the current epic: what "done" means and the cards left. It holds `backlog.md`, the queue, with one line per card that is not yet merged or cancelled; each line names its epic or is marked maintenance. It holds `cards/<name>.md`, one file per card, named by a short descriptive name and not by a number. It holds `journal.md`, with one dated line per event: a card started, merged or cancelled (with the reason), a small edit, a release, a rollback, an incident or a review. It holds `tools/`, with the acceptance script and the measurement script, and the private labelled set. The coordinator commits the work repository after each journal line and pushes it at the end of a session.
- The state of a card is read from where it is: a backlog line without a working copy is queued, a working copy in `git worktree list` is in progress, and a journal line says it was merged or cancelled.
- Working copies live in `.worktrees/<name>/` of the main checkout, which this repository also ignores. The branch of a working copy has the card's name and is never pushed. The main checkout receives only merges and releases, and no one edits its tracked files.
- The live data folder lies outside this repository, and only the untracked `release.local` names it, together with the folder of builds and the checkout to release; `make release` and `make rollback` read it. `make dev` and tests use their own data folders; `/data/` is ignored so a default run cannot add data to a commit.
- `AGENTS.md` holds all the rules that agents follow here. The scripts and any other tools or agent hooks the owner chooses to use are private: they live in the work repository and are switched on in the untracked `.claude/settings.local.json`. The public process does not depend on private tools; where a script is missing, the coordinator does its step by hand.

## Epics, measurement and skeleton

Work is taken from the current epic. Maintenance takes about one card in four unless the owner says otherwise. An epic closes only when the owner accepts a 15-minute demo on the owner's own data, with the measurement next to it. The measurement is a private labelled set of the owner's data and a script in the work repository. Per fact and per verdict, it prints coverage, accuracy and agreement with the owner. First a baseline is taken; then the owner sets thresholds. Once the thresholds are set, `make release` is not run below them. Decisions that wait for a measurement are listed in the handoff note and block the epic that rests on them, not every release.

A card that adds a module, an interface, a table, a route or a kind of background work is preceded by a skeleton card: the spec (the behaviour document and the module list in plain words, approved by the owner), the packages, the interfaces with stubs, and layer rules in `make check`. A layer rule is a check over the package imports that fails the build when a module reaches where it may not: for example, `cmd/sifty` holds no SQL, a use case imports no other use case, and the runner does not import `models`. The rules change only by the owner's decision. A filler card changes neither the spec nor the skeleton; when it needs to, the executor stops and the owner decides. An import cycle is a question for the skeleton, not a reason to narrow a card. `docs/ARCHITECTURE.md` describes the design; the skeleton and the layer rules hold it.

## Cards

A card is one file with the following sections, in this order. It carries no time estimate.

1. **Reason** holds the owner's words as the owner gave them, or the review decision or the defect that started the card.
2. **Result** says what the user sees or gets when the card is done, in the terms of `docs/behaviour/`. For internal work, it says what changes in the code.
3. **Replaces and removes** names the existing code, tables, settings and text that the change replaces, and what is deleted in the same change. An answer of "nothing" needs a reason.
4. **Budget change** gives the expected change of each budget the card touches, code and tests apart, such as "backend code +250, tests +200, one table fewer". The critic's measured change is added at acceptance.
5. **Packages** lists the packages and documents that the card changes.
6. **Checks** says how the result is checked through public boundaries.
7. **Tests** declares the tests, as the section "Tests" below describes.
8. **Preview** is "yes" when the owner wants to see the result before the merge, and "no" otherwise.
9. **Steps** is the short plan.
10. **Report** is written by the coordinator at acceptance. It is the only part the owner reads.

## Life of a card

1. **Backlog.** An idea becomes a line in the backlog that names its epic or is marked maintenance. Ideas come from the owner, from the weekly review, and from defects that anyone finds.
2. **Card.** The coordinator writes the card. When the card changes behaviour that a behaviour document describes, or raises a budget, the owner approves it before work starts. Otherwise no approval is needed. At the start of a card of new functionality, the owner reads its epic and spec, and after the first time only what changed since the last approval. A card that changes what the user sees also changes the behaviour document, in the same merge.
3. **Checks before work.** The test reviewer reviews the Tests section and refuses a hollow one. The critic checks the plan only of skeleton cards and of cards that change an API field, storage, the order at startup or the pinned client, and answers two questions: is this one change, or should it be split; and is its Budget change realistic? The coordinator splits or changes the card when an answer says so. Other cards go straight to work.
4. **Start.** The coordinator starts a card only when its packages do not overlap those of any card in progress, so overlapping cards wait. The migration files, `api/client.ts` and the route table of `server` are shared and do not count as an overlap; the coordinator gives a new migration its number when it brings the branch up to date. The coordinator creates the working copy and the branch from the main branch, records the start in the journal, and starts an executor with its role, the path of its working copy, and the card's text without the Reason and Report sections.
5. **Work.** The executor commits on its branch after each step, so the work survives the end of a session. It writes only the tests the Tests section declares. It asks the test reviewer to review the section before work, and at the end asks only about what the acceptance script routes. It runs `make check` before it reports. Its report says what changed for the user, what was removed, which checks ran and with what result, what was not verified, and which questions are open. When the card is unclear, needs a file outside its Packages, or is a filler card that needs to change the spec or the skeleton, the executor stops and asks.
6. **Merge.** After acceptance, as described in the next section, the coordinator squashes the branch into one commit on the main branch and pushes the main branch. It then removes the working copy and the branch, removes the backlog line and records the merge in the journal.
7. **Cancel.** For a cancelled card, the coordinator removes the working copy, the branch and the backlog line, and records the cancellation with its reason in the journal.

A finding of a review or an audit either joins a card of the current epic, when it touches its area, or comes to the owner as one of at most three decisions at the weekly review. The rest are dropped, not stored. When one area of the application is about to get its third fix within one review period, the weekly review is held at once, before that fix. When a coordinator session ends, the backlog, the journal and the branches hold everything needed to continue. A new session reads them, runs `git worktree list`, and restarts an executor on each card in progress whose report has not arrived, to continue from its commits.

## Acceptance

The coordinator accepts a card once, on the final state of its branch, so the tree it tests is the tree that lands on the main branch.

1. The coordinator brings the branch up to date with the main branch. It resolves a small conflict itself and returns a larger one to the executor.
2. It runs `make uncomment` and commits any changes it makes, then runs `make check`, which builds the application and runs every test and linter.
3. It runs the acceptance script, once per merge; it is a script, not a hook. It compares the branch with the main branch:
   - It takes test names from `go test -list` and cases from `go test -json`, and refuses on any difference from the Tests section in either direction, and on a missing section.
   - A protected test that changed stops the merge; the test reviewer rules on those hunks and the owner sees the verdict. Every changed or deleted assertion of an existing test goes to the test reviewer.
   - It prints the test lines added, helpers and `testdata/` included, beside the estimate; over 50% needs the test reviewer's sentence on why. The coordinator reads the exchange with the test reviewer.
4. It asks the critic to review the branch. The critic reads the final tree after the rebase, the diff and the code around it, and not the card or the report. It checks only what the build cannot, as the critic's rules in `AGENTS.md` say. A violation blocks the merge: it is fixed in the branch, or the owner accepts an exception, which is recorded with its reason as one journal line. The verdict carries the planned size against the size measured by `make budget`, which prints each budget it can measure next to its target; an overrun over 50% needs a sentence on what it bought. A change that goes over a budget is not merged. Either the executor makes the change smaller, or the owner decides to raise the budget, and then the budget table in `docs/ARCHITECTURE.md` changes and an entry naming what the lines are spent on is added to `docs/budget-log.md`, in the same merge.
5. It checks that everything the card names under "Replaces and removes" is gone, and checks the result as the card's Checks section says, through the HTTP (Hypertext Transfer Protocol) interface or in the browser, on `make dev`, which runs the application offline on a copy of the data.
6. When the card asks for a preview, the owner sees the branch run on a copy of the owner's data and opened in the owner's browser, and either accepts it or says what to change. Screenshots are not a preview.
7. When the change is notable, the coordinator adds an entry to `CHANGELOG.md` on the branch, and writes the report: what changed for the user, what was removed, the critic's verdict and budget line, and what was not verified.

When a step fails, the card goes back to its executor with the findings, unless the fix is a small edit, which the coordinator commits on the card's branch.

## Small edits

The coordinator makes small edits itself, without a card or an executor. A small edit is an edit of a document, or a code edit of up to about 20 lines that adds no new logic: no new function, condition, table or setting. The coordinator makes it in its own working copy, `.worktrees/coordinator/`, on its own branch. It runs `make check`, merges the edit like a card, resets its branch to the main branch, and records the edit as one line in the journal.

## Releases

A release is the only event that changes the running application, and it is separate from merging. The coordinator decides to release, whenever the main branch has user-facing changes and `make check` passes, unless the thresholds that the owner set forbid it. The journal records every release and every rollback.

- **Version.** Releases are numbered `MAJOR.MINOR.PATCH` and tagged `vMAJOR.MINOR.PATCH`. MINOR rises when the release has changelog entries, and PATCH rises otherwise. MAJOR rises only by the owner's decision. `sifty version` prints the version of the running build.
- **Changelog.** `CHANGELOG.md` starts with an "Unreleased" section. An entry is added at acceptance for notable functionality, a notable refactoring, or another notable change, such as a change of the data layout or of a budget. Entries are written in the user's terms. A release moves them under its version; a release without entries adds its version with the line "No notable changes".
- **`make release`** works in this order. It refuses to run unless the main checkout is clean and on the main branch. It computes the version and builds the binary into a new release folder outside the repository. It waits until the running application reports no collection in progress. It stops the application, switches it to the new build, starts it, and waits until the application answers its health check; the new build copies the database before it changes its layout (see `docs/ARCHITECTURE.md`). When a step up to the health check fails, it switches back to the previous build, restores that copy if the layout changed, and starts the previous build. Only then does it move the changelog entries, commit, tag and push; a failure of these steps is printed and the steps are repeated with `bash scripts/release.sh publish <version>`, and the application is left running. It keeps the last five builds.
- **`make rollback`** returns to the previous release in one command. When the release did not change the database layout, the command returns to the previous build and nothing is lost. When it did, going back restores the copy made before that release, so everything entered since is lost; there the command refuses, names the time the copy was made and what will go, and runs only as `make rollback CONFIRM=1`. It prints which copy it restored and when it was made. The tag and changelog section of the undone release stay, and the next release takes the next version. After a rollback caused by the application, the coordinator opens a card for the defect and does not release until that card is merged.
- **A change to the interface description.** A merge that changes `api/openapi.yaml` blocks `make release` until a client built against it is pinned: in the client repository `npm run sync-api -- <path to the server>`, `npm run check`, `npm run build`, `npm run release -- <version>`, the tag and the release; then the new version goes into `web/release.txt` on the main branch. A card that only tidies the server leaves `git diff main -- api/openapi.yaml` empty and costs no client release.

## Tests

Tests check behaviour through public boundaries, so the code behind those boundaries can be rebuilt without rewriting the tests. The rules a test follows are in `AGENTS.md`, section "Tests".

- The spec of an epic and every card have a Tests section: three lists (added, changed, deleted), each test written as `TestName — behaviour — the break it catches — N cases`, and the expected test lines; or the word "none". The test reviewer refuses a hollow section: a test without a real break, or "none" on a card that touches a protected behaviour.
- A test is a top-level `Test*`, `Fuzz*` or `Example*` function in any `*_test.go` file of the repository, named by its package and name. A subtest or table row is a case of its test. A rename is a deletion plus an addition.
- When a real defect found during work seems to deserve a test, the executor asks. The coordinator decides, the test reviewer checks the new line sceptically, and the owner sees the amendment in the report.
- The protected behaviours are listed in `AGENTS.md`, section "Critical behaviour"; the test reviewer keeps one or two tests for each.
- Test names contain no card names, card numbers, rule numbers or section numbers. Behaviour that needs a job site is checked against recorded responses behind the fake `Fetcher`. `make check` runs every test; selecting a test by name is for showing that it catches a break, and for nothing else.

## Weekly review

Every week, or after ten merged cards if that comes first, the process is reviewed. The process reviewer measures the process: test lines added per epic against code, defects the owner found after a merge, violations the critic passed that were found later, and the share of cards per epic against maintenance. A check that never fires is a finding against the check. The critic reviews the whole system, as its rules in `AGENTS.md` say. The coordinator gives them `docs/`, the code, the output of `make budget` and how its measures moved, and the cards merged and incidents recorded since the last review. The review also proposes removing a protected behaviour whose code is gone or that no longer meets its criterion. The coordinator turns the results into at most three decisions for the owner, and the owner reads only these decisions.

## Incidents

An incident is a failure in the way the work is done, such as a broken merge, a failed release script, lost work, or a rule that was not followed. A defect of the application is not an incident; it is a card.

- The coordinator recovers at once, for example with `make rollback`, and records the incident as one line in the journal: what happened and what was done. An incident does not create a card at once; the incidents since the last review are looked at together at the weekly review.

## Changing the process

The process consists of this document, `AGENTS.md`, and the scripts behind `make release`, `make rollback` and `make budget`. A new protective rule is added only against a failure that repeated, and it names the rule or mechanism that it replaces. A change to the process is a card like any other: the owner approves it, and it is merged the same way. After the change, the process has the same number of lines or fewer, unless the owner decides it may grow. Agents load `AGENTS.md` (Claude Code does so through `CLAUDE.md`), and the process adds no git hooks and no agent hooks to this repository. The commands named in this document are part of the code. Until a command exists, the coordinator does its steps by hand.
