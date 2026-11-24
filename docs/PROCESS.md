# Development process

This document says how Sifty is developed: who does what, how a piece of work goes from an idea to a release, and how the process itself changes.

## Roles

- The **owner** is the person Sifty is built for. The owner decides what is built, approves every change of described behaviour and of budgets, looks at a preview when a card asks for one, and takes the decisions of each weekly review.
- The **coordinator** is the one Claude Code session that writes to this repository. The owner names it. It writes cards, runs executors, accepts and merges their work, releases, and keeps the work repository. Any other session in this repository only reads.
- An **executor** is a Claude Code agent that the coordinator starts for one card. It works only in its own working copy, on its own branch.
- The **critic** is a Claude Code agent that reads and changes nothing. It checks the plans of cards that add structure, reviews each executor's branch before the executor reports, and reviews the whole system every week.

Three more words are used below. A **card** is the description of one piece of work, from its reason to its report. A **working copy** is a git worktree: a separate folder on its own branch that shares the repository's history. The **work repository** is a private repository that holds the cards, the backlog and the journal.

## In brief

The process rests on six arrangements. Each guarantee is held by one of them, not by checks added on top.

1. **One writer.** Only the coordinator writes to the main branch, and only by merging an accepted card or by releasing. Executors work in parallel, each on its own card, branch and working copy. Because no two writes to the main branch can happen at once, the process needs no locks and no hooks.
2. **Merging is not releasing.** A merge changes the main branch only. The running application changes only through `make release`, which gives the release a version and changelog entries. `make rollback` undoes a release in one command.
3. **Every card names what it replaces.** A card says what it replaces and removes and how it changes the budgets. The coordinator measures the budgets at acceptance, and going over a budget is the owner's decision.
4. **The whole system is reviewed every week.** The critic proposes cards that delete or simplify, and those cards go first in the queue.
5. **The process does not grow by itself.** An incident is recorded and looked at with the others at the weekly review, not patched at once. A new protective rule needs a failure that repeated, and the process grows in size only by the owner's decision.
6. **Code is public and work is private.** This repository holds code and documents only. The cards, the owner's words and the journal live in the work repository.

## Where things live

- This repository (`sifty-works/sifty-server`) is public. It holds the Go server, the tests, `README.md`, `CHANGELOG.md`, `AGENTS.md`, `CLAUDE.md` and `docs/`. `CLAUDE.md` holds one line, `@AGENTS.md`, which makes Claude Code load `AGENTS.md`. The web client lives in `sifty-works/sifty-web`; a versioned release archive is pinned in `web/release.txt` and embedded into the binary by `make build`.
- Nothing from the work repository enters this repository. Code, tests, commit messages and changelog entries describe a change in its own terms and never name a card. Test data is invented.
- The work repository is checked out into `.work/` of the folder that holds this checkout, so it stands beside the application repositories rather than inside one of them, and this repository ignores a `.work/` of its own. It holds `backlog.md`, the queue, with one line per card that is not yet merged or cancelled. It holds `cards/<name>.md`, one file per card, named by a short descriptive name and not by a number. It also holds `journal.md`, with one dated line per event: a card started, merged or cancelled (with the reason), a small edit, a release, a rollback, an incident or a review. The coordinator commits the work repository after each journal line and pushes it at the end of a session.
- The state of a card is read from where it is: a backlog line without a working copy is queued, a working copy in `git worktree list` is in progress, and a journal line says it was merged or cancelled.
- Working copies live in `.worktrees/<name>/` of the main checkout, which this repository also ignores. The branch of a working copy has the card's name and is never pushed.
- The main checkout receives only merges and releases. No one edits its tracked files.
- The live data folder lies outside this repository, and only the untracked `release.local` names it, together with the folder of builds and the checkout to release; `make release` and `make rollback` read it. `make dev` and tests use their own data folders; `/data/` is ignored so a default run cannot add data to a commit.
- `AGENTS.md` holds all the rules that agents follow here. Tools and agent hooks the owner chooses to use are private: they live in the work repository and are switched on in the untracked `.claude/settings.local.json`. The public process does not depend on them.

## Cards

A card is one file with the following sections, in this order. It carries no time estimate.

1. **Reason** holds the owner's words as the owner gave them, or the review decision or the defect that started the card.
2. **Result** says what the user sees or gets when the card is done, in the terms of `docs/behaviour/`. For internal work, it says what changes in the code.
3. **Replaces and removes** names the existing code, tables, settings and text that the change replaces, and what is deleted in the same change. An answer of "nothing" needs a reason.
4. **Budget change** gives the expected change of each budget the card touches, code and tests apart, such as "backend code +250, tests +200, one table fewer". The coordinator adds the measured change at acceptance.
5. **Packages** lists the packages and documents that the card changes.
6. **Checks** says how the result is checked through public boundaries.
7. **Preview** is "yes" when the owner wants to see the result before the merge, and "no" otherwise.
8. **Steps** is the short plan.
9. **Report** is written by the coordinator at acceptance. It is the only part the owner reads.

## Life of a card

1. **Backlog.** An idea becomes a line in the backlog. Ideas come from the owner, from the weekly review, and from defects that anyone finds. Cards from the review go first, and the rest follow the owner's order.
2. **Card.** The coordinator writes the card. When the card changes behaviour that a behaviour document describes, or raises a budget, the owner approves it before work starts. Otherwise no approval is needed. A card that changes what the user sees also changes the behaviour document, in the same merge.
3. **Plan check.** When a card adds a package, a table, a dependency, a configuration key or a kind of background work, the critic reads it before work starts. The critic answers one question: can this be done by replacing or removing something, and does it fit the budgets? The answer goes into the card, and the coordinator changes the card when the answer shows a smaller way.
4. **Start.** The coordinator starts a card only when its packages do not overlap those of any card in progress, so overlapping cards wait. The migration files, `api/client.ts` and the route table of `server` are shared and do not count as an overlap; the coordinator gives a new migration its number when it brings the branch up to date. The coordinator creates the working copy and the branch from the main branch, records the start in the journal, and starts an executor with its role, the path of its working copy, and the card's text without the Reason, Budget change and Report sections.
5. **Work.** The executor commits on its branch after each step, so the work survives the end of a session. It runs `make check` before it reports, and asks the critic to review its branch, in at most two rounds, fixing what it agrees with. Its report quotes the critic's verdict and says what changed for the user, what was removed, which checks ran and with what result, what was not verified, and which questions are open. When the card is unclear, or needs a file outside its Packages, the executor stops and asks.
6. **Acceptance.** The coordinator accepts the work or returns it, as described in the next section.
7. **Merge.** The coordinator squashes the branch into one commit on the main branch and pushes the main branch. It then removes the working copy and the branch, removes the backlog line and records the merge in the journal.
8. **Cancel.** For a cancelled card, the coordinator removes the working copy, the branch and the backlog line, and records the cancellation with its reason in the journal.

A report does not propose a follow-up card, and a finding outside the card goes to the backlog. When one area of the application is about to get its third fix within one review period, the weekly review is held at once, before that fix.

When a coordinator session ends, the backlog, the journal and the branches hold everything needed to continue. A new session reads them, runs `git worktree list`, and restarts an executor on each card in progress whose report has not arrived, to continue from its commits.

## Acceptance

The coordinator accepts a card once, on the final state of its branch, so the tree it tests is the tree that lands on the main branch.

1. The coordinator brings the branch up to date with the main branch. It resolves a small conflict itself and returns a larger one to the executor.
2. It runs `make uncomment` and commits any changes it makes, then runs `make check`, which builds the application and runs every test and linter.
3. It runs `make budget` on the branch and on the main branch and writes the difference into the card. A change that goes over a budget is not merged. Either the executor makes the change smaller, or the owner decides to raise the budget, and then the budget table in `docs/ARCHITECTURE.md` changes in the same merge.
4. It checks that everything the card names under "Replaces and removes" is gone.
5. It checks the result as the card's Checks section says, through the HTTP (Hypertext Transfer Protocol) interface or in the browser. It does this on `make dev`, which runs the application offline on a copy of the data.
6. When the card asks for a preview, the owner looks at the result on `make dev` and either accepts it or says what to change.
7. When the change is notable, the coordinator adds an entry to `CHANGELOG.md` on the branch.
8. It writes the report: what changed for the user, what was removed, how the budgets changed, and what was not verified.

When a step fails, the card goes back to its executor with the findings, unless the fix is a small edit, which the coordinator commits on the card's branch.

## Small edits

The coordinator makes small edits itself, without a card or an executor. A small edit is an edit of a document, or a code edit of up to about 20 lines that adds no new logic: no new function, condition, table or setting. The coordinator makes it in its own working copy, `.worktrees/coordinator/`, on its own branch. It runs `make check`, merges the edit like a card, resets its branch to the main branch, and records the edit as one line in the journal.

## Releases

A release is the only event that changes the running application, and it is separate from merging. The coordinator releases when the owner asks, and only then.

- **Version.** Releases are numbered `MAJOR.MINOR.PATCH` and tagged `vMAJOR.MINOR.PATCH`. MINOR rises when the release has changelog entries, and PATCH rises otherwise. MAJOR rises only by the owner's decision. `sifty version` prints the version of the running build.
- **Changelog.** `CHANGELOG.md` starts with an "Unreleased" section. An entry is added at acceptance for notable functionality, a notable refactoring, or another notable change, such as a change of the data layout or of a budget. Entries are written in the user's terms. A release moves them under its version; a release without entries adds its version with the line "No notable changes".
- **`make release`** works in this order. It refuses to run unless the main checkout is clean and on the main branch. It computes the version and builds the binary into a new release folder outside the repository. It waits until the running application reports no collection in progress. It stops the application, switches it to the new build, starts it, and waits until the application answers its health check; the new build copies the database before it changes its layout (see `docs/ARCHITECTURE.md`). When a step up to the health check fails, it switches back to the previous build, restores that copy if the layout changed, and starts the previous build. Only then does it move the changelog entries, commit, tag and push; a failure of these steps is printed and the steps are repeated with `bash scripts/release.sh publish <version>`, and the application is left running. It keeps the last five builds.
- **`make rollback`** returns to the previous release in one command. When the release did not change the database layout, the command returns to the previous build and nothing is lost. When it did, going back restores the copy made before that release, so everything entered since is lost; there the command refuses, names the time the copy was made and what will go, and runs only as `make rollback CONFIRM=1`. It prints which copy it restored and when it was made. The tag and changelog section of the undone release stay, and the next release takes the next version.
- After a rollback caused by the application, the coordinator opens a card for the defect and does not release until that card is merged.
- **A change to the interface description.** A merge that changes `api/openapi.yaml` blocks `make release` until a client built against it is pinned: in the client repository `npm run sync-api -- <path to the server>`, `npm run check`, `npm run build`, `npm run release -- <version>`, the tag and the release; then the new version goes into `web/release.txt` on the main branch. A card that only tidies the server leaves `git diff main -- api/openapi.yaml` empty and costs no client release.
- The journal records every release and every rollback.

## Tests

Tests check behaviour through public boundaries, so the code behind those boundaries can be rebuilt without rewriting the tests. The rules a test follows are in `AGENTS.md`, section "Tests".

- Test names contain no card names, card numbers, rule numbers or section numbers.
- Behaviour that needs a job site is checked against recorded responses behind the fake `Fetcher`.
- `make check` runs every test; selecting a test by name is for showing that it catches a break, and for nothing else.

## Budgets

The budgets in `docs/ARCHITECTURE.md`, section "Budgets", are a target that the coordinator checks at acceptance. They are not a rule given to executors. `make budget` measures every budget it can on the code and on a fixture data folder, and it prints each measure next to its target. A change over a budget is merged only by the owner's decision, and that decision changes the budget table and adds an entry to `docs/budget-log.md`, naming what the lines are spent on, in the same merge. The weekly review looks at how the measures move over time.

## Weekly review

Every week, or after ten merged cards if that comes first, the critic reviews the whole system. The coordinator gives it `docs/`, the code, the output of `make budget`, and the list of cards merged and incidents recorded since the last review. The critic looks for budgets that are exceeded or growing, two mechanisms doing one job, dead code, code that disagrees with `docs/ARCHITECTURE.md`, and areas that collected several fixes. It returns a list of cards to delete or simplify, each with what it removes and its expected budget change. It also returns a proposal for each incident that repeated. The coordinator turns this into a short list of decisions for the owner, and the owner reads only these decisions. The accepted cards go first in the queue.

## Incidents

An incident is a failure in the way the work is done, such as a broken merge, a failed release script, lost work, or a rule that was not followed. A defect of the application is not an incident; it is a card.

- The coordinator recovers at once, for example with `make rollback`. It records the incident as one line in the journal: what happened and what was done.
- An incident does not create a card at once. The incidents since the last review are looked at together at the weekly review.
- A new protective rule is added only against a failure that repeated, and the new rule names the rule or mechanism that it replaces.

## Changing the process

The process consists of this document, `AGENTS.md`, and the scripts behind `make release`, `make rollback` and `make budget`. A change to the process is a card like any other: the owner approves it, and it is merged the same way. After the change, the process has the same number of lines or fewer, unless the owner decides it may grow. The process uses one tool, Claude Code, and adds no git hooks and no agent hooks to this repository.

The commands named in this document are part of the code. Until a command exists, the coordinator does its steps by hand.
