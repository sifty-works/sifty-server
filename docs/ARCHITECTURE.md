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
- **Modules and mechanisms.** A top-level module is one task of the user and only orchestrates. Every way of doing a job is a worker behind one interface of its module, listed in one place, and a worker calls no other worker. The mechanisms (`db`, `runner` as the queue, `models` as the model library, `fetch`, `config`, `server`, `apperr`, `text`, `facts`) know no module, and a module never imports another module. The queue alone decides order, waiting, retries and what a refusal means: a job gives a task and receives an answer, and a refusal says what it is through the interfaces `runner` defines (`RetryAt() time.Time`, `Final() bool`, `Sentence() string`), so `runner` does not import `models`. A job kind names the kinds that follow it when it registers (`Then`). A model task is data shipped with the application, `models/tasks/<name>/` with `prompt.md` and `schema.json`, and `work.txt` names its kind of work. `cmd/sifty` only wires. SQL is written only in the store part of a module (`repo*.go`, `store*.go`) and in the mechanisms that own tables.
- The rules are in `layers.txt` at the repository root: which package is a mechanism, which belongs to which module (named by the package it goes into), which are workers, and a line for each deviation of today's code with the change that removes it. `scripts/layers.sh` compares it with `go list` and the source files, and `make check` fails on an import or SQL call the rules do not allow, a package the file does not list, a deviation without its change, and a deviation nothing uses. The file changes only by the owner's decision.
- **Errors are typed:** `Invalid` → 422, `NotFound` → 404, `Conflict` → 409, `Busy` → 423, anything else and panics → 500 with "Internal error, see log #id" and a log entry. A Go error text never reaches the user outside Monitor › Logs, which shows the log's warnings and errors as written. Held keys are replaced by `***` when a line is written, in the log file and on standard error, and again when the log is read; lines written before are scrubbed in both log files at start, before the log opens. Only the keys held now are scrubbed and masked: the key of a provider removed from the settings, or an earlier value of `secrets.env`, stays in old lines. The store returns codes; texts for the user live in the service or the client.

## Capabilities and packages

| Module | Packages today | Package it goes into | Behaviour |
|---|---|---|---|
| Collection | `source` (worker), `listing`, `catalog` (including permanent deletion and the exclusion predicate), `collect` | `collection` | `behaviour/collection.md` |
| Analysis | `analysis` (workers, one list), `reading`, `places`, `skills` | `analysis` | `behaviour/assessment.md` |
| Fit | `fit` (`Comparer`, `Verdict`) | `fit` | `behaviour/assessment.md` |
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
func SaveReading(ctx context.Context, db *db.DB, vacancyID, version int64, readerVersion string, rec reading.Record) error // replaces any earlier reading for the vacancy
```

- `Fetcher` is the only way to job sites, vacancy pages, employers' career pages and link documents: public-address checks on every redirect, size limit, timeout, per-domain spacing and back-off, and a memory of blocked domains (`domain_block`) that survives a restart. The form-reading browser uses the same per-domain spacing and `domain_block`. Model adapters and the web search client have their own clients.
- Site quirks are data in `SiteConfig` (content selector, link pattern, list address, skip JSON-LD), so a site-specific source is usually a configuration line over the generic source. The applicant-tracking-system client is shared by collection and by the employer search.
- `Detail` is called only when the listing has no text and the posting is new, or known without text and not requested in the last 7 days, and never while its domain is blocked; `Catalog.Known` tells which. The audit proposed requesting every known vacancy every 7 days to detect changes; that is not done, because a vacancy with text is never requested again.
- `Posting` carries the structured fields a source states (salary, employment type and the like) as a compact `source_fields` value, which the reader uses before asking a model.
- Sources are read in parallel, one after another within one requested host (boards sharing a Greenhouse, Lever or Ashby API form one group). Each source's result is written to `source_run`.

### Assessment

```go
type Reader interface { Read(ctx context.Context, in VacancyInput) (VacancyReading, error) } // one model call per text
func Read(ctx context.Context, m models.Model, in ReadInput) (Record, string, error)          // builds request, calls model, validates; returns record and reader version
func LocateQuote(plainText, quote string) (start, end int, ok bool)                           // package text: fold-based excerpt location; typographic variants (smart quotes, dashes, zero-width marks) match
func Score(p ProfileView, v VacancyReading, r Rules) Fit                                      // pure, no I/O
type ShortlistChecker interface { Check(ctx context.Context, f ProfileFacts, evidence string, v VacancyReading) (Check, error) }
type RequirementChecker interface { /* reserved; connected only after it beats code on labelled examples */ }
```

- `VacancyReading` holds conditions (money, work modes, engagement, hours, locations, time zone) and requirements (text, level, alternatives, quote as offsets). Code reads `source_fields` and labels first; a stored reading fills what code left empty; the model is asked only for the rest, and every value must cite an exact excerpt. A reading is keyed by vacancy, version and `reader_version`; a new `reader_version` queues rereading at low priority.
- `Read` builds a prompt and schema for the missing conditions only, calls the model at most twice (retrying when every requirement fails validation or the reply is empty on a text that states some), and validates every quote with `text.LocateQuote`. A quote that is not found drops its item; at most forty requirements are kept, required before preferred. Work-mode, engagement and hours conditions are accepted only when the quote speaks of a place of work, a contract kind or working time. The reader version is `reading/1+<8-hex-chars>`, the hex being a prefix of the SHA-256 of the embedded prompt and schema files.
- `Score` computes Fits you, the requirements counter, the four tiles and the requirement lines in one place. It takes about 0.01 ms per vacancy, so the breakdown is computed when a vacancy is opened. The list stores what it filters and sorts on in the `fit` table: the state and rounded number of Fits you, with the vacancy version and a 64-bit hash of the profile's inputs (effective goals, skills, confirmed facts, work authorizations, weights, rates, the skills vocabulary and a scoring version) it was computed from; a result is current while both match, and a result that is not current shows dimmed and counts as Not scored. The row also keeps a hash of the version and the listing fields code reads besides the text (title, location, tags, `source_fields`) and the stored reading's `reader_version` and `created`, which SQL cannot compute; the job compares it in Go, so a listing change without a text change is recomputed but not dimmed. `recompute_fit` (cpu lane) recomputes in passes only the results that are missing, not current or with a changed listing hash, and repeats until a pass writes nothing, so a change made while it runs is not lost; it is queued at start, after each collection run, and after My data or a profile is saved or created. When `recompute_fit` finishes, it calls `QueueReadings`, which enqueues a `read_vacancy` job (Serial) for each visible unread vacancy (priority 0) or each visible vacancy whose reading is from an older reader version (priority −1); it does nothing when offline, when no reading provider is configured, or when a `read_vacancy` job is already queued or running. The counts ✓, ✕ and ? join the same row when requirements are read and are empty until then; the state the user sees is derived from them and from the reading job that is queued or running.
- `ShortlistChecker` gets `EvidenceText` (see Profiles); the digest of its inputs covers the vacancy version, the projections it used and the evidence text. A changed digest marks the check outdated; it runs again when the vacancy page is opened.
- `places` is the one resolver of countries, regions, remote restrictions, onsite country and time-zone bands. Its vocabulary, the embedded `places.yaml`, holds the countries with the bands they span, the regions with their aliases and countries, the words of unrestricted remote work, the separators and qualifiers of composite labels, the six bands with their offsets and the time-zone abbreviations; Go code only looks them up. There is no city table; cities are free text.
- `skills` is the one implementation of the skills vocabulary and of splitting requirement text into skills.

### Inbox and vacancy page

- One SQL query builds a list. For Inbox: the profile's visibility (target roles matched as whole words in the `title` column of `vacancy_fts`; keywords and "All of these" terms matched as whole words across `{title tags text}` columns) over vacancies with no decision in the profile. For Shortlist, Applied and Dismissed: the vacancies with that decision. Exclusions apply to all four. The query joins applications and the `fit` row and sorts on the server, by Best fit or Newest in Inbox and Shortlist and by the date of the decision in Applied and Dismissed; filters (Fit, Published, Sources, Unread) are applied in the same query, and the counts of Sources are one `GROUP BY` over the same set. The Fit filter has no counts.
- **Search** keeps substring semantics over title, company, tags and text. Title, company and tags are matched in SQL with `instr`. The query is normalised (lowercased, non-word runs to one space) and matched in the `text` column of the full-text index; a query shorter than three characters matches title, company and tags only. Decompressing and scanning the bodies in Go was measured at 235 ms for 2,958 vacancies and replaced; the index answers in under 6 ms.
- The vacancy view is assembled in one package from the vacancy, its reading and `Score`; it says which actions are available and why.

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

- Saving writes one JSON record and calls one `changed` callback, which queues `recompute_fit`. Consumers compare the hash of the projection they used, so only what changed is recomputed: a new keyword changes visibility only, a new skill recomputes Fits you of every profile, a new compensation of one profile recomputes only that profile. When visibility grows, vacancies that became visible without a reading are queued for reading.

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

- One table `job(id, kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)` runs every kind of background work: `collect`, `read_vacancy`, `recompute_fit`, `shortlist_check`, `locate`, `prepare`, `analyze_document`, `retention`. Finished jobs are kept for 90 days. A failed job's `error` holds a sentence made from the error's type when it fails; the Go text goes to the log with the job's id.
- **Lanes are taken per stage, not per job:** `browser` (1) for reading a form, `net` (several) for collection and search, `cpu` (1) for recomputation. A model is not a lane; a stage that needs one takes the model gate per call and holds no lane while it waits, so reading a form never waits for a model.
- **Restart:** a job that was running becomes `interrupted`. Automatic kinds (`read_vacancy`, `recompute_fit`, `shortlist_check`, `retention`) are queued again while they have had fewer than three attempts, else `failed`. `locate` is queued again as a boards-only search. `collect`, `prepare` and `analyze_document` stay `interrupted` until the user retries; for `prepare` this follows the decision that failed preparations are not retried automatically.
- **Cancel:** the job states are `queued`, `running`, `done`, `failed`, `interrupted` and `cancelled`. A cancelled job is a decision of the user and is answered at once: a queued job never starts, and a running job has its context cancelled without waiting for it to notice. The saved `params` of a cancelled job are kept, so retrying an `analyze_document` takes the job over and continues from the saved chunk, as it does for an `interrupted` one. A restart does not queue a cancelled job again.
- **Retries:** one policy, exponential pause, at most three attempts.
- **Schedule:** a ticker every five minutes queues `collect` when automatic collection is on and the interval since the start of the last run has passed. Loading the application calls `POST /api/collect` without `force`, which applies the same rule at once. "Collect jobs" calls it with `force`. `retention` is queued daily.
- **Status:** one endpoint or event stream replaces separate polls of queues, analysis and health. The list reloads only when decisions or visibility change; a new fit result reloads only visible rows or the open vacancy.

## Models

```go
type Model interface { Complete(ctx context.Context, r Request) (Response, error) }
type Request  struct { Prompt string; Schema json.RawMessage; Temperature *float64; Think string }
type Response struct { Text string; InTokens, OutTokens int }
```

- **Adapters:** `ollama` (with its own loader per instance), `openai` (any OpenAI-compatible endpoint, local or remote with an API key), `cli-claude`, `cli-opencode`. A registry maps a type to a constructor; a new provider is one file and one line. Command-line adapters run with a reduced environment and tools turned off, and reject an answer that tried to call a tool.
- **Kinds of work:** `reading` (vacancy texts, bulk; also profile documents), `shortlist_check`, `preparation`. Each has its own provider in settings.
- **Every call goes through `models.For(kind, class)`**, which wraps the model in three layers:
  1. **dispatcher**: user actions first, background calls pre-empted through `ctx`; one call at a time per model; every adapter honours `ctx`. A background call waits, with `not_before` on its job, while background model work is paused, while the day's count in `model_call` has reached the daily limit, and while the provider does not answer its check;
  2. **cost barrier**: each provider has `cost: local | free | paid`; bulk work on `paid` is refused unless the user allowed paid bulk work;
  3. **journal** `model_call`: time, kind, provider, model, characters and tokens in and out, duration, outcome, vacancy id, kept for 90 days. It is how the budgets below are measured.
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
| `vacancy_reading` | the model's reading of a vacancy version with its `reader_version`: conditions and requirements, quotes as offsets |
| `fit` | profile, vacancy, vacancy version, hash of the profile's inputs, hash of the listing fields, state, Fits you (stored for SQL sorting and filtering); the requirements score, counts ✓ ✕ ? and reading state join it when requirements are read |
| `shortlist_check` | profile, vacancy, digest of inputs, result, date |
| `decision` | profile, vacancy, stage, reason, date; no row means Inbox |
| `application` | vacancy, date applied, profile and its name at the time |
| `me`, `profile` | My data and each profile as JSON |
| `document`, `proposal`, `qa` | profile documents (files under `<data>/files/`), proposed facts, Questions & Answers |
| `application_form`, `application_field`, `learned_phrasing` | last reading of a form, its fields with values and their source, learned labels |
| `job` | background work |
| `model_call` | journal of model calls |
| `settings` | key-value table (key TEXT PRIMARY KEY, value TEXT) for what the application remembers about its use: active profile, pause of background model work |

**Size of a vacancy** (measured on the imported catalogue, then estimated): row with body 3.1 KB, sources 0.4 KB, full-text index 11 KB (trigram with positions, needed for phrase matching; without positions the candidates must be checked in Go, which costs 100 to 200 ms for common words), reading 0.8 to 1.2 KB, fit and decisions 0.5 KB: about 17.5 KB.

**Retention.** A daily `retention` job applies the rule in `behaviour/settings-and-data.md`. It runs in two phases; the first is live, the second is planned: (1) **Expiry removal:** permanently deletes vacancies whose `expires_at` passed more than a day ago and that have no application row, using `catalog.Delete` so tombstone keys are written. (2) **Text retention (planned):** drops `body`, the index entry and the reading of the vacancy for unseen old vacancies; about 0.5 KB of row, sources and decisions stays. A row without `body` always takes the text when a source shows it again. With about 55,000 new vacancies a year, growth falls from about 1 GB to about 30 MB a year for retained rows, plus the active window of about 160 MB.

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
| Model calls for a change of profile or My data | 0 for Fits you and the counter; Shortlist checks become outdated and run again only when their vacancy is opened |
| Shortlist check | at most 2 per shortlisted vacancy on entering Shortlist, and 2 per opening after its inputs changed (one retry of a reply that decides nothing) |
| Preparation of one form | 0 to 1 to classify labels, 1 to 2 to draft (one retry of an empty reply); Write and Paste 1 each |
| Network per collection run | 1 request per source (2 for Hacker News), plus 1 per vacancy without text in the listing that is new, or known without text and not requested in the last 7 days, plus 1 per posting without a company that is new, or whose stored vacancy has none and was not asked in the last 7 days; a posting that is still without a company is not stored, so every run offers it again and requests its page again: a known cost, made visible by the count of postings without an employer on the source |
| List API | under 50 ms at the 95th percentile for 1,200 rows; vacancy view under 30 ms; decision under 20 ms |

Size by capability (backend, then frontend, in lines), measured for a capability that is delivered and estimated for the rest: collection 4,450 and 400 (fetching, the listing model and its normalisation, the catalogue, sources, collection runs, and data lines such as heading lists and site configurations); assessment 3,100 and 700; inbox 1,600 and 1,500; profiles 2,700 and 2,350; application 2,800 and 800; foundation 3,700 and 900 (settings pages, with the Monitor logs page). That is about 18,350 and 6,650, so the backend keeps a reserve of 150 lines: a capability that needs more takes it from another by the owner's decision. The frontend estimate is about 450 lines over its budget; the profile editors are where it must be recovered, with one generic section editor instead of one editor per section. The frontend budget is checked in sifty-web by its `scripts/budget.sh`.
