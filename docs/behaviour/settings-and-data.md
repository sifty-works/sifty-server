# Settings, data and models

What the user gets: the application is one program that starts on an empty data folder with no setup, keeps everything it stores in that one folder on the user's machine, contacts only the sites and services listed below, and lets the user choose a provider for each kind of model work. Nothing paid is used in bulk without the user's explicit permission.

## Where data lives

- Everything the application stores lives in one folder, chosen by `--data-dir`, then the `SIFTY_DATA_DIR` environment variable, then a `data` folder beside the working folder when one is already there, and otherwise the application folder of this user: `Library/Application Support/Sifty` on macOS and `.config/sifty` elsewhere. The path is made absolute. The folder is created with access for the user who runs the application only. The browser that reads application forms uses its own installation outside this folder.
- Only a browser on this machine can use the application; other web pages cannot make it act. A second copy started on the same data folder refuses to start.
- **What leaves the machine:**
  - requests to the job sites of the sources, and to the pages of vacancies;
  - requests to public applicant tracking boards and to employers' sites, including application forms read by the browser;
  - requests to the web search provider the user configured;
  - requests to pages the user adds as link documents;
  - prompts to the model providers the user configured.
- There is no telemetry.

## First start

The application is one program to run; reading application forms additionally needs Node.js and Playwright on the machine (see `application.md`), and reading the text of a PDF needs poppler (`pdftotext`), which a document card names with its install command when it is missing. It starts on an empty data folder without copying any template. Every key of the settings file is optional; a missing key takes its default. The first run shows empty lists and a hint to add sources, and model work shows "Configure a provider" until one is set.

## Where settings live

- **The settings file**, one file in the data folder, holds what the user configures: sources, the collection interval and switch, providers and the provider of each kind of model work, "Allow paid bulk work", the daily limit of background model calls, the web search provider, weights and rates.
- **The database** holds what the application remembers about its use: the active profile and the switch that pauses background model work.
- The description of known sites is kept in `<data>/vocab/sites.yaml` when the user wants to override it; otherwise the application uses an embedded description.
- Forms in Settings edit all of these. Advanced shows the settings file only.

## Recovery at start

A settings file or a site description file that cannot be parsed or fails validation is never fatal. The application renames the broken file to `<name>.broken-<time>`, uses the newest valid backup of the settings file or the embedded description for site descriptions, and starts normally. Each recovery is printed at start and listed under `recovered` in the status response. A file that cannot be read (permissions, I/O error) is not renamed. An unknown key in the settings file, as may appear after a rollback to an older version, is ignored. A file that is put aside because of a refused value gives the same sentence the settings screen gives for that value, for example "The age limit is a whole number of months, from 1 to 1200, or empty for no limit.", and that sentence is what the recovered notice shows and the start prints.

## Settings

- **Sources:** the sources, the automatic collection switch and the interval (see `collection.md`).
- **Exclusions:** the one list of exclusions (see `inbox.md`).
- **Models:**
  - the providers, each marked local, free or paid;
  - a provider for each kind of model work: **Reading** (facts and requirements of vacancies, and facts of profile documents), **Shortlist check**, and **Preparation** (form labels, drafts, cover letters, pasted forms);
  - the state of each provider and a "Check" button that sends a fixed test prompt with no user data;
  - "Allow paid bulk work", off by default;
  - a switch that pauses background model work, and a daily limit of background model calls.
- **Web search:** the provider used to find employers' pages (Tavily, Brave, DuckDuckGo, or none), with its key taken from the environment or the secrets file. With none, the employer search uses only applicant tracking boards.
- **Background work:** what is running, what is waiting and why, and what failed in the last 7 days, each failure with its reason as a sentence (see `application.md`).
- **Advanced:** the settings file in a text editor, next to the forms. Saving checks the file first and keeps a backup of the saved content in the data folder; the last ten backups are kept, and the editor names the folder where they are. A change made in a form keeps the comments of the settings file.

A change applies at once without a restart, except a change of the port. A change that affects results (weights) starts a background recomputation and the answer returns at once.

**Debug mode**, started with `--debug` or `debug: true` in the settings file, shows the diagnostic panels of application preparation (see `application.md`).

## Model providers

- The first version supports four kinds of provider: **Ollama**, **OpenAI-compatible** (local or remote, with an API key), **Claude CLI** and **opencode CLI**. Anthropic models are reached through the Claude CLI or an OpenAI-compatible endpoint.
- Each kind of model work uses its own provider. There is no silent fallback to another provider, and no fallback to a paid one.
- Data leaves the machine only to the providers the user configured.
- Bulk work (reading vacancy texts) refuses a provider marked paid, unless "Allow paid bulk work" is on.
- A user's action goes ahead of background model work. One model serves one call at a time.
- A provider that does not answer (it cannot be reached, loads no model, returns an error status, or its command line is not signed in or gives no text) holds background model work for five minutes at a time (Settings › Background work shows the hold and until when), and then the next background call tries it again by itself; its success lifts the hold. A failure of one request — a prompt longer than the context, a command-line answer that is not what was asked for, or an answer in which the model tried to call a tool — fails only that piece of work and holds nothing, unless three requests in a row fail this way, which holds the provider like one that does not answer; any success starts the count again. Background work also waits when the pause switch or today's calls cannot be read, and every call that may have been paid counts against the daily limit, even one the user cancelled.
- **API keys** come only from the environment or from a secrets file in the data folder, read at start without overriding variables already set. Saving settings that contain a key in plain text is refused.
- **Command-line providers** run with a reduced environment and with tools, extensions and project settings turned off. An answer in which the model tried to call a tool is rejected.
- **Ollama:** the model is loaded once before the first call and kept loaded for an hour by default; loading may take up to 600 seconds and shows "Loading model <name> · m:ss"; the context size follows the length of the prompt, and a prompt that would be cut is refused; loaded models are unloaded when the application stops.
- A prompt longer than the provider's context gives an explicit error, never a silently shortened prompt.

## Keeping data

- An expired vacancy is removed automatically once a day when its expiry date has passed by more than a day and no application has been recorded for it, in any profile. A vacancy with an application stays and shows "Listing expired". Dismissed vacancies without an application are removed the same way. Removal leaves a mark of the vacancy's addresses so collection keeps it away. A vacancy is also removed manually by "Delete role…" (see `inbox.md`).
- **Text retention.** Once a day, the text of a vacancy is removed when the vacancy is in no profile's Shortlist or Applied, was never opened or is dismissed in every profile that decided it, and has not been seen at any source for 60 days. The model's reading of that text goes with it. The row stays: title, company, addresses, dates and decisions. The vacancy page says "Text removed after 60 days unseen; it returns if the source shows the role again", and search finds it only by its title, company and tags. When a source shows it again, its text is fetched again and its decisions stay.
- A full-text index of vacancy texts is kept for search.
- Of My data and of each profile only the latest version is kept; there is no history of edits.
- The application is for one user; it keeps no user accounts.
- Changes of the database layout are applied automatically at start, inside one transaction, after the application has copied the database into the backups folder of the data folder; the last five copies are kept. An older version of the application refuses to open a database of a newer layout.
- **Backup.** `sifty backup [--data-dir <folder>] [--out <file>]` writes one `.tar.gz` archive, by default `sifty-backup-<UTC time>.tar.gz` in the working folder, and prints its path and size. It holds a consistent copy of the database (taken while the application runs, and without changing the database), the settings file, the site descriptions file `vocab/sites.yaml` when the user has one, and the `files` folder (documents and photo). It leaves out the secrets file, earlier database copies, logs, the lock and everything else in the folder, and says what it left out. Unpacked into an empty folder, the archive is a data folder the application opens with the same vacancies, profiles and documents. The command refuses to write over an existing file and refuses a folder with no database, each with a sentence that names the path.

## Errors

- An error is shown to the user in words, with what to do when there is something to do. An internal failure shows a short message with a reference to the log and is written to the log with its details; it never shows a stack or an internal value.
- An unknown address of the server's interface answers "Refresh the page. Sifty was updated and this address is no longer used."
- When the server cannot be reached, the frontend shows "Sifty is not running. Start it and refresh the page."
- A request that takes 500 ms or more, other than a model call, is written to the log.

## Not carried over

- Copying templates by hand before the first start, the four configuration files that had to exist, and the separate frontend and worker folders that had to sit next to the program.
- The candidate configuration file and the Settings › Candidate context screen (see `profiles.md`).
- The assessment queue page with its switch and its table (see `assessment.md`); the Background work page replaces it.
- The "Extraction" stage label that promised a fallback no code used, the hidden matching stage, and settings that were read and never applied.
- The order of background work following the sort.
- Prompt copies in the configuration folder that nothing read.
- Loopback-only HTTP providers; remote providers with an API key are allowed.
- A lock on the local model shared between several application processes on one machine; one process uses one model.
- Revision histories of My data, profiles and drafts.
- Answers and corrections kept as Markdown files in the data folder; they live in the database.
- Unlimited configuration backups.
- Raw error values and panic texts shown in the interface.

## Data from the old application

The import reads a copy of the old database, opened read-only, and writes into an empty data folder; it refuses an unknown database version and makes no network request and no model call. It carries over what the user made and the model readings still in use: vacancies with their ids, structured fields, sightings, aliases and removal marks; exclusions; My data and profiles; the active profile; documents and their files, with the suggestions from them that are still to be reviewed; the photo; Questions & Answers; learned label phrasings; decisions, applications and read marks; drafts; model readings of vacancy conditions and requirements; corrections written on vacancies, which become Confirmed by you; and the secrets file. A vacancy whose best text after merging is not full is not carried over unless a profile has a decision, application, draft or correction on it; it leaves no removal mark. A vacancy whose employer cannot be found, by its own company or from its address, is not carried over either, with its decisions and applications, and the import report counts how many were left out. One pool of documents replaces the documents of each profile: a file, page or note that hangs on several profiles is carried once, and the report counts the repeats. No document is trusted as evidence after the import; "Use as evidence" is off until the user turns it on. A deleted document is not carried, nor is a suggestion already accepted or rejected, nor a suggestion of a kind the server no longer proposes (the report names each such kind); a suggested target role of a profile that is not the active one is not carried either, because accepting it would write it into the wrong profile. A document whose file is missing from the old folder is carried without it, as unavailable, and the report counts it. The old answers folder, empty in use, is not imported. The old configuration files become one settings file holding only the values that differ from the defaults, and the import report lists them. The full-text index and Fits you are rebuilt. Page originals, snapshots, old candidate contexts, old detailed assessments, model matching results, intermediate result tables, run journals (except the start time of the last collection run, which keeps the interval) and locks are dropped. The import report counts every kind of record before, after and dropped, with the reason, and gives a SHA-256 of every copied file. The old weights of Fits you and exchange rates are carried into the settings file where they differ from the defaults. The old provider configuration — providers with their type (ollama, openai-compatible, Claude CLI, opencode CLI), model and endpoint; the work stages mapped to Reading and Preparation; and the allow-paid-bulk flag, daily limit and paused switch — is carried into the settings file and the database where it differs from the defaults.

**Fits you compared with the previous version.** The import recomputes Fits you of every current old result (the result of the current profile and My data, of the current text, and of the rules most of the profile's results used) with the imported vacancy, the profile's goals and the imported weights and rates. Its report gives the number of results that are equal (same state and same number), different by an expected difference, different and unexplained, and not comparable (the vacancy or the profile was not imported), and the number of old results that were not current. A difference is expected only when the cause of every differing criterion, or of a number on one side only, is a named expected difference; the report counts each code and the unexplained criteria. `<data>/fit-comparison.tsv` has one row per difference: profile, vacancy, old and new value, each differing criterion with its code or `?`, and the row's codes or `unexplained`.

**Comparison before switch-over.** The old application keeps running unchanged, apart from security fixes, until switch-over. Until then the new one runs daily on a fresh copy, in its own data folder and on another port, with background work paused, only to compare. The comparison runs twice:

1. With the weights of the new criteria (onsite work authorization and requirements) set to zero: per profile, the list ids, order and stages; twenty saved searches; Fits you of every vacancy; and a hundred vacancy pages. Every difference is explained.
2. With the new settings: the same comparison against a list of expected differences written beforehand (see `inbox.md`).

Switch-over is: stop the old application, run the final import, check it, start the new one on the old port. The old data folder stays untouched for a rollback; there is no double writing, so work done in the new application after switch-over is lost on a rollback.
