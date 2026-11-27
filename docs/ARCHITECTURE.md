# Architecture

How Sifty is built. What the user sees is in `docs/behaviour/`; this document says how the code, the data and the background work are arranged to deliver it, and what limits the design must stay within.

## Design rules

These rules come from what made the previous application grow to several times the size its behaviour needed.

1. **One representation per fact.** A vacancy's text is stored once, as sanitised HTML. Plain text, sections, facts derived by code and the score breakdown are computed when read, not stored next to their source. A derived value is stored only when the list must filter or sort on it in SQL, and the table says so.
2. **Change is a version, not a hash in every table.** A vacancy has a `version` that rises when its normalised text changes. Everything derived from the text is keyed by `(vacancy_id, version)`, so no other table hashes the text and no stage has to freeze another to keep its cache valid.
3. **One mechanism per job.** One way to the job sites (`Fetcher`), one background runner, one model gate, one list of exclusions, one stage per vacancy and profile, one settings file.
4. **The server decides, the client shows.** Verdicts, thresholds, available actions and texts of states are computed on the server; the client never overrides them.
5. **A table needs a reader and an owner.** A table is added only when some code reads it and one capability owns it. Deleting a vacancy cascades to everything that belongs to it.
6. **Start does no work over the catalogue.** Anything that can be derived is derived when read, or queued as low-priority background work.
7. **A new feature names what it replaces.** A layer is not added next to the one it supersedes; the older one is removed in the same change.
8. **Where there can be more than one way, the ways are a list.** When a job has, or may later have, more than one way of being done — a kind of file, a model provider, a kind of source, a way to read a text — each way is a module behind one interface and the choice is data: one entry in one list, read by one caller. A way is added or withdrawn by its entry, never by editing the caller, and the rest of the system does not change with it. Two branches of a switch over kinds say the list is missing. Two questions find such a place and either one is enough: what a second way would cost today, and whether the next case will be written by copying this one. The second catches what the first misses, because a shape that is copied multiplies without any new way arriving.

## Process and layers

- Two repositories: **sifty-server** (this repository, Go, `github.com/sifty-works/sifty-server`) holds the server binary; **sifty-web** (`github.com/sifty-works/sifty-web`) holds the TypeScript frontend. A versioned web release archive is pinned in `web/release.txt`; `make web` downloads, verifies its SHA-256 and unpacks it into `web/dist`; the Go embedding package (`web/`) embeds that tree into the binary. A build without `web/dist` serves the API alone with a placeholder page.
- One process, written in Go, with SQLite (pure Go driver) and the web client embedded in the binary. A second server on the same data folder is refused by a lock file `<data>/sifty.lock`.
- The server listens on `127.0.0.1` only, checks Host and Origin, requires JSON for every request that changes something, and limits a body to 1 MB except document uploads. Requests of 500 ms or more, other than model calls, are logged.
- Each capability has three layers: **handler** (parses and checks input, calls the service, writes JSON) → **service** (domain rules) → **repo** (SQL only, typed rows and typed errors). A foundation package `db` opens the database, runs migrations and makes backups; no other package reaches the raw connection. It keeps a pool of four connections, so a read does not wait for a write while a connection is free; a transaction takes the write lock when it begins and waits for another writer inside SQLite, up to the busy timeout of five seconds.
- **Modules and mechanisms.** A top-level module is one task of the user and only orchestrates. Every way of doing a job is a worker behind one interface of its module, listed in one place, and a worker calls no other worker. The mechanisms (`db`, `runner` as the queue, `models` as the model library, `fetch`, `config`, `server`, `apperr`, `text`, `facts`) know no module, and a module never imports another module. The queue alone decides order, waiting, retries and what a refusal means: a job gives a task and receives an answer, and a refusal says what it is through the interfaces `runner` defines (`RetryAt() time.Time`, `Final() bool`, `Sentence() string`), so `runner` does not import `models`. A job kind names the kinds that follow it when it registers (`Then`). A model task is data shipped with the application, `models/tasks/<name>/` with `prompt.md` and `schema.json`; `work.txt` names its kind of work and `item.txt` what the user calls the text it sends. `cmd/sifty` only wires. SQL is written only in the store part of a module (`repo*.go`, `store*.go`) and in the mechanisms that own tables.
- The rules are in `layers.txt` at the repository root: which package is a mechanism, which belongs to which module (named by the package it goes into), which are workers, and a line for each deviation of today's code with the change that removes it. `scripts/layers.sh` compares it with `go list` and the source files, and `make check` fails on an import or SQL call the rules do not allow, a package the file does not list, a deviation without its change, and a deviation nothing uses. The file changes only by the owner's decision.
- **Errors are typed:** `Invalid` → 422, `NotFound` → 404, `Conflict` → 409, `Busy` → 423, anything else and panics → 500 with "Internal error, see log #id" and a log entry. A Go error text never reaches the user outside Monitor › Logs, which shows the log's warnings and errors as written. Held keys are replaced by `***` when a line is written, in the log file and on standard error, and again when the log is read; lines written before are scrubbed in both log files at start, before the log opens. Only the keys held now are scrubbed and masked: the key of a provider removed from the settings, or an earlier value of `secrets.env`, stays in old lines. The store returns codes; texts for the user live in the service or the client.

## Capabilities and packages

| Module | Packages today | Package it goes into | Behaviour |
|---|---|---|---|
| Collection | `source` (worker), `listing`, `catalog` (including permanent deletion and the exclusion predicate), `collect` | `collection` | `behaviour/collection.md` |
| Analysis | `analysis` (workers, one list), `places`, `skills` | `analysis` | `behaviour/assessment.md` |
| Fit | `fit` (`Comparer`, `Verdict`; one comparer per criterion) | `fit` | `behaviour/assessment.md` |
| Lists | `inbox` (list, decisions, exclusion management, vacancy view) | `lists` | `behaviour/inbox.md` |
| Profiles | `candidate` (My data, profiles, documents, proposals, Questions & Answers) | `profiles` | `behaviour/profiles.md` |
| Application | `shortlistcheck`, `locate`, `websearch`, `formread`, `answer` (the last four planned) | `application` | `behaviour/application.md` |
| Upkeep | the daily retention run and the journal cleanup, now in `cmd/sifty` | `upkeep` | `behaviour/settings-and-data.md` |
| Mechanisms | `apperr`, `db`, `config`, `runner`, `models`, `fetch`, `server`, `text` (quote location and text folding), `facts` (the types Analysis hands to Fit); `cmd` only wires | the same | `behaviour/settings-and-data.md` |

### Collection

```go
type Fetcher interface { Get(ctx context.Context, url string, opt Opt) (Response, error) } // errors: ErrBlocked, ErrNotFound, ErrTransient, ErrRefused, ErrOffline
type Source interface {
    Match(u *url.URL) bool
    List(ctx context.Context, f Fetcher, s SiteConfig) ([]Posting, error)
}
func Detail(ctx context.Context, f Fetcher, p Posting, s SiteConfig) (Posting, error) // fills text; only called where the listing has none
func Normalize(raw string) string                               // the only path from any raw HTML to stored HTML
func (c *Catalog) Upsert(ctx context.Context, p Posting, src string) (int64, Outcome, error) // New|Updated|Unchanged|Merged|Tombstoned, one transaction
```

- `Fetcher` is the only way to job sites, vacancy pages, employers' career pages and link documents: public-address checks on every redirect, size limit, timeout, per-domain spacing and back-off, and a memory of blocked domains (`domain_block`) that survives a restart. The form-reading browser uses the same per-domain spacing and `domain_block`. Model adapters and the web search client have their own clients.
- Site quirks are data in `SiteConfig` (content selector, link pattern, list address, skip JSON-LD), so a site-specific source is usually a configuration line over the generic source. The applicant-tracking-system client is shared by collection and by the employer search.
- `Detail` is called only when the listing has no text and the posting is new, or known without text and not requested in the last 7 days, and never while its domain is blocked; `Catalog.Known` tells which. The audit proposed requesting every known vacancy every 7 days to detect changes; that is not done, because a vacancy with text is never requested again.
- `Posting` carries the structured fields a source states (salary, employment type and the like) as a compact `source_fields` value, which the reader uses before asking a model.
- Sources are read in parallel, one after another within one requested host (boards sharing a Greenhouse, Lever or Ashby API form one group). Each source's result is written to `source_run`.

### Analysis

```go
type Worker interface { Name() string; Version() int; Produces() []facts.Kind; Extract(ctx context.Context, doc *Doc) ([]facts.Fact, error) }
func Split(doc *Doc) ([]Section, []Span)                                      // the sections stage: the heading map applied to the stored body
func Analyse(ctx context.Context, database *db.DB, load Loader, id, version int64) error
```

- Analysis loads the stored text of a vacancy (its body, and `listing.PlainText` of it as `Doc.Original`, which `cmd/sifty` supplies through the `Loader`), splits it into sections, runs the workers of its one list in order (site fields, conditions, requirement lines, key facts) and saves what each found through `facts`. It calls no model and writes nothing to `vacancy`; the heading map is applied when analysing, so `listing.Normalize` and the stored text are unchanged. The heading list of `listing` is for display only.
- A section is named by the heading that opens it, found as h2–h4 in the stored body, as a paragraph wholly in bold, or as a short line ending in a colon, or a known heading inside a paragraph (after a sentence end, before a colon, or a multi-word one before a capital letter, a digit or a bullet; "Perks and benefits;", "BENEFITS: …", "QualificationsBachelor's …"; the text before it stays in the earlier section): `role`, `requirements`, `nice_to_have`, `offer`, `company`, `process` (the `offer` sections are only skipped by the workers that read requirements and conditions); the text before the first heading is `intro` and a heading the map does not know opens `other`. The map is the embedded `sections.yaml`: normalised exact headings, "contains" keywords, and "about <the vacancy's company>". A heading the map does not know that lies deeper than its parent stays inside the parent section.
- `fact` rows hold vacancy, version, worker, kind, value, flag (required, preferred), the quote's position, who found it (site field or vocabulary; aggregator or employer, from the vacancy's address) and the worker's version. Positions are offsets in `listing.PlainText` of the stored body; a change of `PlainText`, of the heading map or of a vocabulary raises the versions of the workers that use it. Every worker runs for every kind; the orchestrator then chooses between the values by one rule and saves only the effective facts, so `facts.Load` returns what is saved, for each vacancy with the current version only, together with its `analysed` rows: when the address is an aggregator's, a value stated in the vacancy text (a quote position, from a labelled line or a vocabulary) replaces a site field's value of the same kind, and when it is the employer's own board, a site value (field or labelled line) replaces the vocabulary's value of that kind. A worker's rows are replaced as a set, together with one `analysed` row that records the version it ran at and a digest of the rows it saved, which changes when its facts change and is what `fit` compares (the Shortlist check compares those of the workers whose facts it reads, so a worker it does not read leaves its stored checks current); a vacancy is pending when a worker has no `analysed` row at its current version. Money and hours values are JSON; a required language is a kind of its own.
- A place found in the text must be a recognised one (a country, region, worldwide, or a city of the `places` city list, alone or followed by its state or country), read item by item from lists, parentheses and the segments of a pipe-separated header line, and never from a part marked preferred; a work-mode cue in the offer or company sections counts only when no other section states one, and a language counts as required only with a cue of using it. Requirement lines come from the `requirements` and `nice_to_have` sections only, flagged required or preferred by the section and by marker words, and, in a section that has list items, not from prose after its last list; a requirement paragraph with inline bullets is read as one line per bullet; a text with no such section, or whose such sections give no line, uses its lines that state a requirement. A line that reads as pay, benefits, perks, equal-opportunity or accommodation text, or a sentence about how to apply, the hiring process or inclusion, or a line of hashtags, is never a requirement line. A term met only in the company, offer or process sections is not a requirement and a language met there is not a required language.
- The key facts worker reads eight kinds from phrases of the embedded `keyfacts.yaml`: the most years the requirement lines ask (`years`), the seniority from the title and then from narrow cues in the text (`seniority`), the company stage with the latest raise from the intro and company sections (a role section that opens the text counts as intro) (`company_stage`), and, from the whole text, the deadline for applications as an ISO date (`deadline`; a date without a year takes the year nearest to the day the vacancy was first seen), the length of a contract with its unit, a range kept as a range (`contract_length`: "6 months", "16 weeks", "4–8 months"), a citizenship or clearance requirement as the text names it, or by a name of the vocabulary for U.S. persons, export controls and public trust positions (`clearance`), and `relocation` and `visa`, each `offered` or, when the text denies it, `none` or `refused`, a sentence that asks the applicant a question not being read, and a denial being read from the words before the phrase up to the last "but" and from those just after it (`terms.go`). Years are read with `skills.YearsIn`, the one reading of "N years" (its largest match counts). Its tiles exist only for a stated value, carry no goal and join the others after them; the Experience tile compares the years with the derived years of My data, Apply by counts the days from the profile view's `Today`, which is read when the vacancy is opened and is not part of the inputs hash, and no tile of these enters Fit.
- The job `analyze_vacancy` (one at a time, resumed after a restart) analyses one vacancy. `cmd/sifty` queues it for every vacancy that is visible in at least one profile, has its full text and is pending: at start, after each collection run (the follow-up of `collect`) and when My data or a profile is saved. Which vacancies are pending is decided in `analysis`; the condition of being visible is handed to it by the wiring, not imported from the lists. Its follow-up is `recompute_fit`.

### Assessment

```go
func FromFacts(title string, full bool, set facts.Set) Vacancy                                // Fit's own view of the facts of a vacancy
func LocateQuote(plainText, quote string) (start, end int, ok bool)                           // package text: fold-based excerpt location; typographic variants (smart quotes, dashes, zero-width marks) match
func Score(p ProfileView, v Vacancy, r Rules) Fit                                              // pure, no I/O
type Comparer interface { Name() string; Compare(v Vacancy, p ProfileView, s Strictness) Verdict } // Verdict: Outcome (match, mismatch, unknown, skipped), Utility, Source, Reason
type ShortlistChecker interface { Check(ctx context.Context, f ProfileFacts, evidence string, v Vacancy) (Check, error) }
type RequirementChecker interface { /* reserved; connected only after it beats code on labelled examples */ }
```

- `Vacancy` holds what `fit` needs of the facts: money, work modes, engagement, hours, places, time zone, required languages and the requirement lines with their flag (required or preferred); money and hours are decoded from the JSON the facts carry. It also keeps, for each kind of fact, the source that states it: a fact with a quote in the text counts as stated by the text, a fact without one is a field of the listing. A vacancy without `analysed` rows is not analysed: its counts stay empty and the list says so. Requirement lines are decided in `fit` against My data: a confirmed fact first, then a line that asks for years of experience (undecided, with the derived years as evidence), then the skills vocabulary of `skills`, which only matches text to skills groups.
- `Score` runs the one list of comparers (`Comparers`: role, money, location, remote restriction, time zone, work mode, engagement, hours, languages, requirements), each of which gives a match, a mismatch, unknown or skipped (the profile states no goal), a utility and a reason. Languages and remote restriction are must by name in code; work mode, engagement, hours and location compare the vacancy's values with the profile's buckets (Preferred 1, Possible 0.75, Unwanted 0.25 as a mismatch, Never a broken must, a value in no bucket leaves the criterion out), and the best value of the vacancy counts. The number is the weighted mean in which an unknown criterion with a weight enters as utility 0.5 at half its weight, and a vacancy of which no criterion was compared has no number; a mismatch on a must caps it at 15, or at 40 when the fact is only an aggregator's field (`CapBroken`, `CapWeak`), and only the first sets the stored flag that puts the vacancy last in Best fit. A fact with no stated source counts as weak. `Score` also computes the tiles (verdict and line from the comparers; the key-facts tiles have none) and the requirement lines in one place. It takes about 0.01 ms per vacancy, so the breakdown is computed when a vacancy is opened. The list stores what it filters and sorts on in the `fit` table: the state and rounded number of Fit and whether a must is broken, with the vacancy version and a 64-bit hash of the profile's inputs (effective goals, skills, confirmed facts, work authorizations, languages, weights, rates, the skills vocabulary and a scoring version) it was computed from; a result is current while both match, and a result that is not current shows dimmed and counts as Not scored. The row also keeps a hash of the version and the listing fields code reads besides the text (title, location, tags, `source_fields`) and the digests of the vacancy's `analysed` rows, which SQL cannot compute; the job compares it in Go, so a listing change without a text change is recomputed but not dimmed. `recompute_fit` (cpu lane) recomputes in passes only the results that are missing, not current or with a changed listing hash, and repeats until a pass writes nothing, so a change made while it runs is not lost; it is queued at start, after each collection run, and after My data or a profile is saved or created. `recompute_fit` runs after the analysis jobs: the runner does not pick it while an `analyze_vacancy` job is queued or running. The `analysed` flag joins the same row and is set when the vacancy is analysed; the analysis state a list row shows is derived from it and from the analysis job that is queued or running.
- `ShortlistChecker` gets `EvidenceText` (see Profiles); the digest of its inputs covers the vacancy version, the projections it used and the evidence text. A changed digest marks the check outdated; it runs again when the vacancy page is opened.
- `places` is the one resolver of countries, regions, remote restrictions, onsite country and time-zone bands. Its vocabulary, the embedded `places.yaml`, holds the countries with the bands they span, the regions with their aliases and countries, the words of unrestricted remote work, the separators and qualifiers of composite labels, the six bands with their offsets and the time-zone abbreviations; Go code only looks them up. There is no city table; cities are free text.
- `skills` is the one implementation of the skills vocabulary and of splitting requirement text into skills.

### Inbox and vacancy page

- One SQL query builds a list. For Inbox: the profile's visibility (target roles matched as whole words in the `title` column of `vacancy_fts`; keywords and "All of these" terms matched as whole words across `{title tags text}` columns) over vacancies with no decision in the profile. For Shortlist, Applied and Dismissed: the vacancies with that decision. Exclusions apply to all four. The query joins applications and the `fit` row and sorts on the server, by Best fit or Newest in Inbox and Shortlist and by the date of the decision in Applied and Dismissed; filters (Fit, Published, Sources, Unread) are applied in the same query, and the counts of Sources are one `GROUP BY` over the same set. The Fit filter has no counts.
- **Search** keeps substring semantics over title, company, tags and text. Title, company and tags are matched in SQL with `instr`. The query is normalised (lowercased, non-word runs to one space) and matched in the `text` column of the full-text index; a query shorter than three characters matches title, company and tags only. Decompressing and scanning the bodies in Go was measured at 235 ms for 2,958 vacancies and replaced; the index answers in under 6 ms.
- The vacancy view is assembled in one package from the vacancy, its facts and `Score`; it says which actions are available and why.

### Profiles

```go
Effective(p, base Profile) Profile          // inheritance from the first profile, at read time
Goals(p) GoalsView; Evidence(me) []Fact      // inputs of Score; fit hashes what it uses
EvidenceText(me, p, docs) string             // Shortlist check and preparation; never contacts, legal name or form answers
Visibility(p) Filter
FormAnswers(me) map[string]string            // contacts, legal name, form answers: application only
type TextSource interface { Text(ctx context.Context, body []byte) (string, error) } // stored bytes in, text out
```

- A pdf is read by `TextSource`, whose one implementation runs an external program (`pdftotext - -` of poppler, bytes on standard input, text on standard output) through the launcher of the command-line model adapters (`models.RunCLI`): reduced environment, the caller's timeout (30 seconds), and a cap on the output (4 MiB for a pdf, 8 MiB for a model). It is not a model call and passes no gate. A missing program leaves the document `unavailable` with a reason that names the install command; a failed, stopped or silent program leaves it `error` with a reason and no text. `POST /api/documents/{id}/refresh` reads a pdf that has no text again from the stored file, and reads a link's page again; a way of taking in a document that cannot be read again refuses it. The server does not install the program. Another program is another implementation or the same one with another command.

- Saving writes one JSON record and calls one `changed` callback, which queues `recompute_fit`. Consumers compare the hash of the projection they used, so only what changed is recomputed: a new keyword changes visibility only, a new skill recomputes Fit of every profile, a new compensation of one profile recomputes only that profile. When visibility grows, the vacancies that became visible are queued for analysis before the recompute, in the same callback.

### Application

```go
type Locator    interface { Locate(ctx context.Context, v Vacancy, deep bool) (Link, error) } // boards, then web search; no model
type WebSearch  interface { Search(ctx context.Context, q string) ([]Result, error) }       // Tavily, Brave, DuckDuckGo, or none
type FormReader interface { Read(ctx context.Context, url string) (Form, error) }            // browser; PasteReader has the same shape
type Answerer   interface { Answer(ctx context.Context, v Vacancy, f Form, have Answers) (Answers, error) } // Profile → Classifier → Drafter
```

- Entering Shortlist queues `locate`. When `locate` ends, it queues `prepare`, which reads the form from the employer's page or the original listing, then answers. A page found later by a manual `locate` queues `prepare` again. `locate` and `prepare` are deduplicated per vacancy, so no two searches of one vacancy run at once.
- `ProfileAnswerer` is a pure function; its answers are stored with `source=profile`. `Classifier` makes one batched call for unknown labels and caches them. `Drafter` selects evidence once and drafts all model fields in one call, with the shared context first so a prompt prefix cache applies.
- An edit by the user updates one field row with a revision check.

## Background work

```go
type Spec struct { Kind, Subject string; Priority int; Params json.RawMessage }
type Runner interface {
    Enqueue(Spec) (int64, error)          // deduplicated by (kind, subject) among queued and running
    Cancel(id int64) error                // stops a queued or running job and leaves it `cancelled`
}
```

- One table `job(id, kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)` runs every kind of background work: `collect`, `analyze_vacancy`, `recompute_fit`, `shortlist_check`, `locate`, `prepare`, `analyze_document`, `retention`. Finished jobs are kept for 90 days. A failed job's `error` holds a sentence made from the error's type when it fails; the Go text goes to the log with the job's id.
- **Lanes are taken per stage, not per job:** `browser` (1) for reading a form, `net` (several) for collection and search, `cpu` (1) for recomputation. A model is not a lane; a stage that needs one takes the model gate per call and holds no lane while it waits, so reading a form never waits for a model.
- **Restart:** a job that was running becomes `interrupted`. Automatic kinds (`analyze_vacancy`, `recompute_fit`, `shortlist_check`, `retention`) are queued again while they have had fewer than three attempts, else `failed`. `locate` is queued again as a boards-only search. `collect`, `prepare` and `analyze_document` stay `interrupted` until the user retries; for `prepare` this follows the decision that failed preparations are not retried automatically.
- **Cancel:** the job states are `queued`, `running`, `done`, `failed`, `interrupted` and `cancelled`. A cancelled job is a decision of the user and is answered at once: a queued job never starts, and a running job has its context cancelled without waiting for it to notice. The saved `params` of a cancelled or failed job are kept, so retrying an `analyze_document` takes the job over (a failed one with its attempts counted afresh) and continues from the saved chunk, as it does for an `interrupted` one. A restart does not queue a cancelled job again.
- **Retries and refusals:** one policy, exponential pause, at most three attempts. The queue alone decides what an error means, from the interfaces `runner` defines: an error with a `RetryAt` makes the job wait until then without counting an attempt; a `Final` error fails the job at once with its `Sentence`; any other error is retried, and after the third attempt its `Sentence`, if it has one, is the failure text, else "Failed; the log has the details under job #n". A refusal that is final and also has a `RetryAt` (paid bulk work, refused until the owner allows it) fails a kind at once unless the kind registers `Patient`, which makes it wait: `analyze_document` is patient, `shortlist_check` is not. A job body decides none of this; `Job.Waits` only tells it whether the queue will wait, so that it leaves a document's state alone.
- **Order and follow-ups:** a kind names the kinds that follow it when it registers (`Then`), may register `Follow`, a function that enqueues follow-ups that depend on data (`collect` queues the analysis of the vacancies it brought), and may declare `After`, kinds it waits for: the runner does not pick it while a job of one of them is queued or running. A `Then` follow-up is deduplicated against queued jobs only, so a recompute that is already running does not swallow the one that facts finished during it needs, and thousands of analyses collapse into one waiting recompute. The runner runs follow-ups only when the job ended well, and an error of `Follow` fails the job like any other. A job body never enqueues; handlers (a user's action) and the scheduler do. `collect` is followed by the analysis and then `recompute_fit`; `analyze_vacancy` is followed by `recompute_fit`, which runs `After` `analyze_vacancy`. At start and when My data or a profile is saved, the analysis is queued and then `recompute_fit`.
- **Schedule:** a ticker every five minutes queues `collect` when automatic collection is on and the interval since the start of the last run has passed. Loading the application calls `POST /api/collect` without `force`, which applies the same rule at once. "Collect jobs" calls it with `force`. `retention` is queued daily.
- **Status:** one endpoint or event stream replaces separate polls of queues, analysis and health. The list reloads only when decisions or visibility change; a new fit result reloads only visible rows or the open vacancy.

## Models

```go
type Model interface { Complete(ctx context.Context, r Request) (Response, error) }
type Request  struct { Prompt string; Schema json.RawMessage; Temperature *float64; Think string }
type Response struct { Text string; InTokens, OutTokens int }
```

- **Adapters:** `ollama` (with its own loader per instance), `openai` (any OpenAI-compatible endpoint, local or remote with an API key), `cli-claude`, `cli-opencode`. A registry maps a type to a constructor; a new provider is one file and one line. Command-line adapters run with a reduced environment and tools turned off, and reject an answer that tried to call a tool.
- **Kinds of work:** `reading` (profile documents, bulk; the vacancy text is read by code and the kind is kept for the model that will read what code leaves), `shortlist_check`, `preparation`. Each has its own provider in settings.
- **Every call goes through `models.For(kind, class)`**, which wraps the model in three layers:
  1. **dispatcher**: user actions first, background calls pre-empted through `ctx`; one call at a time per model; every adapter honours `ctx`. A background call waits, with `not_before` on its job, while background model work is paused, while the day's count in `model_call` has reached the daily limit, and while the provider does not answer its check;
  2. **cost barrier**: each provider has `cost: local | free | paid`; bulk work on `paid` is refused unless the user allowed paid bulk work;
  3. **journal** `model_call`: time, kind, provider, model, characters and tokens in and out, duration, outcome, vacancy id, kept for 90 days. It is how the budgets below are measured.
- **Tasks:** `shortlist_check` and `document_proposals` are tasks. `Task.Ask` sends the prompt through the model it is given, decodes the JSON answer into the caller's value and asks once more when the answer cannot be read; a second unreadable answer is a final error, so an unreadable model costs a job two calls, never more. Checks of meaning (a quote found, something found where the text states it) stay with the caller, which may ask again. A task's kind of work is the one the gate is asked for.
- **Refusals state themselves:** `ErrOffline`, `ErrNoProvider`, `ErrBulkOnPaid` (final, with a retry time of ten minutes), `ErrPromptTooLong`, `ErrProviderUnavailable`, `ErrUnusableAnswers` and `WaitError` give `RetryAt`, `Final` and `Sentence`; `Task.Ask` and `Gate.Ready` put the words of the task and of the kind of work into the sentence, so the user's path (opening a shortlisted vacancy) and the Monitor show the sentence the error carries.
- There is no lock on a model shared between processes; one process uses one model.

## Data

| Table | Holds |
|---|---|
| `vacancy` | id, identity, title, company, addresses, location, tags, `source_fields`, dates, first and last seen, `seen_at`, apply address and employer-search state, `content_hash`, `version`, text state, `detail_requested_at`, `body` (sanitised HTML, compressed) |
| `vacancy_fts` | contentless full-text index (`content=''`, `contentless_delete=1`), three columns `title`, `tags`, `text`; each column holds the pre-normalised text (lowercased, non-word runs collapsed to one space, padded with leading and trailing space) so that a phrase query with its own padding matches whole words |
| `vacancy_source` | where and under which address a vacancy was seen |
| `vacancy_alias` | old id → id after a merge |
| `tombstone` | canonical address of a removed vacancy, or a legacy hash: the SHA-256 of an address under the previous application's canonical rule, `listing.LegacyKey`, which the catalogue checks next to the new canonical address |
| `exclusion` | scope (all profiles or one), kind (word or company), value |
| `domain_block` | blocked domain and until when |
| `source_run` | per run and source (keyed by the source's normalised address): status, counts found, new, updated, unchanged, excluded, removed, and error |
| `fact` | vacancy, version, worker, kind, value, flag, quote position, way and origin, worker version: what analysis by code found; rows go with their vacancy |
| `fit` | profile, vacancy, vacancy version, hash of the profile's inputs, hash of the listing fields, state, Fit and the broken-must flag (stored for SQL sorting and filtering); the analysed flag, which tells a row's analysis state, joins it |
| `shortlist_check` | profile, vacancy, digest of inputs, result, date |
| `decision` | profile, vacancy, stage, reason, date; no row means Inbox |
| `application` | vacancy, date applied, profile and its name at the time |
| `me`, `profile` | My data and each profile as JSON |
| `document`, `proposal`, `qa` | profile documents (files under `<data>/files/`), proposed facts, Questions & Answers |
| `application_form`, `application_field`, `learned_phrasing` | last reading of a form, its fields with values and their source, learned labels |
| `job` | background work |
| `model_call` | journal of model calls |
| `settings` | key-value table (key TEXT PRIMARY KEY, value TEXT) for what the application remembers about its use: active profile, pause of background model work |

**Size of a vacancy** (measured on the imported catalogue, then estimated): row with body 3.1 KB, sources 0.4 KB, full-text index 11 KB (trigram with positions, needed for phrase matching; without positions the candidates must be checked in Go, which costs 100 to 200 ms for common words), fit and decisions 0.5 KB, facts not yet measured: about 16 KB without facts.

**Retention.** A daily `retention` job applies the rule in `behaviour/settings-and-data.md`. It runs in two phases; the first is live, the second is planned: (1) **Removal:** permanently deletes, with one query, vacancies that have no application row and either expired more than a day ago or were decided in no profile and published (or, undated, first seen) before the collection age limit (`collection.max_age_months`; with no limit nothing is removed by age), using `catalog.Delete` so tombstone keys are written. (2) **Text retention (planned):** drops `body`, the index entry and the facts of the vacancy for unseen old vacancies; about 0.5 KB of row, sources and decisions stays. A row without `body` always takes the text when a source shows it again. With about 55,000 new vacancies a year, growth falls from about 1 GB to about 30 MB a year for retained rows, plus the active window of about 160 MB.

**Migrations** are numbered SQL files embedded in the binary. At open: read `user_version`; refuse a newer one with a clear message; when migrations are pending, copy the database with `VACUUM INTO` into `<data>/backups/` (the last five are kept); run each migration with its `user_version` in one transaction.

## Configuration

- One file, `<data>/sifty.yaml`, holds what the user configures: sources (keyed by their normalised address), the collection interval and switch, providers and their cost, the provider of each kind of work, paid bulk permission, the daily model limit, the web search provider, weights and rates. Every key is optional; an unknown key is ignored and reported. Defaults are one Go function; `sifty config defaults` prints the full file, so no template is stored and no personal value is in the repository.
- Vocabularies (places, skills, sections, form phrasings, site descriptions) are embedded data of their package. Only the site description is overridable: `<data>/vocab/sites.yaml` is merged by host over the embedded one, so new sites added by a release still arrive, and an override entry that fails validation renames the file and uses the embedded description alone. A vocabulary gains an override when a card gives it one, not by itself.
- A `sifty.yaml` that fails to parse or validate at start is renamed to `<name>.broken-<time>`; the newest valid backup is used, or defaults if none. A file that cannot be read is not renamed, and a refused file that cannot be renamed is left as it is. Each recovery appears in `GET /api/status` under `recovered` as a sentence in `text`.
- What the application remembers about its use lives in the `settings` table, not in the file.
- Secrets come only from the environment or `<data>/secrets.env`; saving a plain-text key in the file is refused.
- One `Validate`. Saving is parse → validate → backup of the bytes written (the last ten are kept, enough to undo a day of edits without unbounded growth) → atomic write → swap in memory; a form edits the YAML node so comments stay; when a step fails nothing is written.
- `--debug` or `debug: true` turns on diagnostic panels.

## Build and commands

- `make web`: downloads the pinned web client archive, verifies its SHA-256 (refuses and deletes on mismatch), and unpacks it into `web/dist`; skipped when `web/dist` already holds the pinned version. `make build` = `make web` + `go build`.
- `make dev`: starts `sifty serve --offline` on a copy of the data (port 7300) and prints how to run the web client (`npm run dev` in a sifty-web checkout). `--offline` forbids every network request and model call. `SIFTY_REPLAY_DIR=<folder>` adds `--replay <folder>`, which serves requests from recorded responses instead of making real HTTP calls.
- `make release`: runs `make web`, then refuses when `web/dist/api.sha256` (the SHA-256 of the description the web client was built against) differs from the SHA-256 of `api/openapi.yaml`, then runs the release steps `docs/PROCESS.md` describes: the build, the switch of the running application with its way back, the tag and the push. `make rollback` returns to the previous build.
- `make check`, `make budget`, `make rollback`: see `PROCESS.md`. Neither runs npm.
- Commands: `serve` (`--offline`, `--replay <folder>`, `--port`, `--debug`, `--data-dir`), `backup` (database copy and the user's files in one archive), `config defaults`, `version`.

## Web client

The web client lives in `github.com/sifty-works/sifty-web`. Its source is in that repository; this repository embeds a released archive.

- `api/schema.gen.ts` (in sifty-web): TypeScript types generated from `api/openapi.yaml` by `openapi-typescript`; kept up to date by `npm run check` in sifty-web. `api/client.ts`: one typed function per endpoint, using the generated types as its type aliases. `state/`: the list cache by profile, stage and query; decisions of the session (decided rows kept on screen until reload; the decision answer replaces the row; decision p95 under 20 ms); filters, query and sort remembered in the browser. `views/`: shell, tabs, list, row, filters, vacancy view, fit band, ask and offer cards, listing, Shortlist check, dialogs, profile pages, settings pages. `keyboard.ts`: keys by physical position.
- Views do not call the API directly and compute no verdict or threshold.
- To develop the client against a local server: run `make dev` in sifty-server (starts server on port 7300), then `npm run dev` in a sifty-web checkout (Vite proxies `/api` to port 7300).
- To release a new web client: `npm run release -- <version>` in sifty-web builds the archive and writes `dist/api.sha256`; update `web/release.txt` in sifty-server with the new version, URL and SHA-256; `make release` in sifty-server verifies the description match before building.

## Endpoints

`api/openapi.yaml` is the one description of the HTTP interface. It is generated from the Go types by `sifty gen-api` and kept up to date by `make check`. The table below summarises the full endpoint set; the implemented routes are described by the generated schema.

| Group | Endpoints |
|---|---|
| Lists and vacancies | `GET /api/vacancies?profile&stage&q&sort&filters&limit&offset` (a window of the list, 100 rows of about 350 bytes by default; counts per stage and per filter value, and `matching`, counted over the whole selection); `GET /api/vacancies/{id}?profile`; `POST /api/vacancies/{id}/decision {profile,from,to,reason?,confirmed?}`; `POST /api/vacancies/{id}/seen`; `DELETE /api/vacancies/{id}?profile`; `POST /api/vacancies/{id}/refresh` |
| Exclusions | `GET/POST/DELETE /api/exclusions?profile` (word exclusions take an optional profile scope; company exclusions are global), `GET /api/exclusions/preview` |
| Collection | `GET/POST/PATCH/DELETE /api/sources`; `PUT /api/sources/schedule`; `POST /api/collect {force}` |
| Profiles | `GET/PATCH /api/me`; `PUT/DELETE/GET /api/me/photo` (png, jpg or webp, at most 2 MiB, real type checked from bytes); `GET/POST/PATCH/DELETE /api/profiles`; `GET /api/profiles/{id}`; `PUT /api/active-profile`; `POST /api/me/skills` (+ and − from a vacancy) |
| Documents | `POST /api/documents` (file upload, pasted text or a link); `GET /api/documents` (list, newest first); `DELETE /api/documents/{id}`; `PATCH /api/documents/{id}` (set evidence); `GET /api/documents/{id}/text` (extracted text); `GET /api/documents/{id}/original` (stored file); `POST /api/documents/{id}/analyze`, `POST /api/documents/{id}/refresh`; `GET /api/proposals` (to review, newest first); `POST /api/proposals/{id}/accept`, `/reject` |
| Questions & Answers | `GET/POST/PATCH/DELETE /api/qa` |
| Reference | `GET /api/reference` (countries, regions and time-zone bands from the `places` vocabulary, and option sets; shipped as embedded data) |
| Application | `GET /api/vacancies/{id}/application`; `PATCH …/application/fields/{field}`; `POST …/application/{write,paste,questions,retry}`; `POST /api/vacancies/{id}/locate`; `POST /api/vacancies/{id}/shortlist-check/retry` |
| Settings and status | `GET/PUT /api/settings`, `GET/PUT /api/settings/file`; `GET/PATCH /api/models` (providers with their cost and state, the provider of each kind of work, paid bulk permission, the pause switch, the daily limit and the day's background calls); `POST /api/providers/{name}/check`; `GET /api/work` (running jobs, and queued jobs by kind with the reason they wait); `GET /api/logs` (failures of the last 7 days, and the last 200 warnings and errors of the log with keys masked); `GET /api/status` (or event stream); `GET /api/version` |

## Budgets

These are targets the coordinator checks at acceptance of every change (see `PROCESS.md`). Going over one needs the owner's explicit decision, and every change of this table is recorded in `docs/budget-log.md` with what the lines are spent on.

| Measure | Budget |
|---|---|
| Backend code | at most 18,500 lines |
| Frontend code | at most 6,200 lines |
| Database per vacancy | at most 18 KB |
| Model calls for a new vacancy text | 1 |
| Model calls for a change of profile or My data | 0 for Fit; Shortlist checks become outdated and run again only when their vacancy is opened |
| Shortlist check | at most 2 per shortlisted vacancy on entering Shortlist, and 2 per opening after its inputs changed (one retry of a reply that decides nothing) |
| Preparation of one form | 0 to 1 to classify labels, 1 to 2 to draft (one retry of an empty reply); Write and Paste 1 each |
| Network per collection run | 1 request per source (2 for Hacker News), plus 1 per vacancy without text in the listing that is new, or known without text and not requested in the last 7 days, plus 1 per posting without a company that is new, or whose stored vacancy has none and was not asked in the last 7 days; a posting that is still without a company is not stored, so every run offers it again and requests its page again: a known cost, made visible by the count of postings without an employer on the source |
| List API | under 50 ms at the 95th percentile for 1,200 rows; vacancy view under 30 ms; decision under 20 ms |

Size by capability (backend, then frontend, in lines), measured for a capability that is delivered and estimated for the rest: collection 4,450 and 400 (fetching, the listing model and its normalisation, the catalogue, sources, collection runs, and data lines such as heading lists and site configurations); assessment 3,100 and 700; inbox 1,600 and 1,500; profiles 2,700 and 2,350; application 2,800 and 800; foundation 3,700 and 900 (settings pages, with the Monitor logs page). That is about 18,350 and 6,650, so the backend keeps a reserve of 150 lines: a capability that needs more takes it from another by the owner's decision. The frontend estimate is about 450 lines over its budget; the profile editors are where it must be recovered, with one generic section editor instead of one editor per section. The frontend budget is checked in sifty-web by its `scripts/budget.sh`.
