# Sifty Server

Sifty is a personal job-search application that runs on your own machine. It collects vacancies from the job sites you choose, shows each one once with clean readable text, scores how well it fits the goals of each of your search profiles, and prepares draft answers for the application forms of the vacancies you shortlist. Its browser extension puts the answers into the form on the employer's page; you attach files, give consent and submit. Sifty never submits anything.

Your data stays in one folder on your machine. Sifty sends requests only to the job sites you list, to the web search providers you configure, and to the model providers you choose: a local model through Ollama or any OpenAI-compatible server, a remote OpenAI-compatible service with your own API key, or the Claude or opencode command-line tools. Scoring is done by code; a model is used only to read the requirements of a vacancy once, to check a shortlisted vacancy, and to draft application answers.

## Why Sifty

Sifty exists for two jobs.

1. **Finding and navigating vacancies.** One list of vacancies from many sites, each shown once, with clean text and a score of how well it fits what you are looking for.
2. **Writing the hard answers.** Application forms ask questions such as "Why do you want to work for us?", "Describe your experience with technology X" or "What interests you most in your work?". A good answer rests on your real experience and on what this employer is looking for, as the vacancy describes it.

Today people write these answers in a general chat assistant: they paste the vacancy into the chat, get a generic first draft, then go back and forth to remove the filler, add facts about themselves and narrow the focus. Sifty brings this work into one place. It already holds the vacancy and your data, drafts an answer from both, and lets you correct it. Every answer you finish is kept, together with what it says about your experience, values and way of working, so each next question, for the next company, starts from more of what is true about you.

Filling names, e-mail addresses and links is not the point: the browser already does that.

## Repositories

- **sifty-server** (this repository): the Go server binary. `api/openapi.yaml` describes the HTTP interface.
- **sifty-web** (`github.com/sifty-works/sifty-web`): the TypeScript web client. A versioned release archive is pinned in `web/release.txt`; `make web` downloads, verifies and unpacks it.

## Running the server

```
make build          # download the pinned web client, then go build
make dev            # server on port 7300 (offline, with a fixture data folder)
```

## Releasing and rolling back

1. Copy `release.local.example` to `release.local` (untracked) and set the data folder of the running application, the folder that holds builds and the checkout to release. The port is read from `sifty.yaml` in the data folder.
2. `make release` builds, switches the running application to the new build and tags the release; `make rollback` returns to the previous build. `docs/PROCESS.md` describes both.

## Browser extension

Each release carries the Chrome extension in `extension/` beside the binary (in a checkout, the `extension/` folder of this repository). It fills the application form on the employer's page with the answers Sifty prepared; you attach files, give consent and submit.

1. Open `chrome://extensions` and turn on Developer mode.
2. Choose Load unpacked and select the `extension` folder.
3. Open the extension's options and set the address of Sifty if it is not `http://127.0.0.1:8080`.
4. Open an application page and press the Sifty button in the toolbar.

The extension's id is fixed by the key in its manifest, so Sifty accepts it without any setting.

## Developing the web client against a local server

1. Run `make dev` in this repository (starts the server on port 7300).
2. In a checkout of `github.com/sifty-works/sifty-web`, run `npm run dev`.
   Vite proxies `/api` to `http://127.0.0.1:7300`.

## Releasing a new web client

1. In sifty-web, run `npm run release -- <version>` to build the archive.
2. Update `web/release.txt` in this repository with the new version, URL and SHA-256.
3. `make release` verifies that `web/dist/api.sha256` matches `api/openapi.yaml` before building.

## Documents

- `docs/behaviour/` describes what the user sees and gets, one document per area:
  - `collection.md`: sources, collection runs, vacancy text, duplicates;
  - `assessment.md`: the Fit score, requirements, the Shortlist check;
  - `inbox.md`: the lists, search, filters, decisions, the vacancy page;
  - `profiles.md`: My data, profiles, documents and suggested facts;
  - `application.md`: application forms, draft answers, background work;
  - `settings-and-data.md`: settings, model providers, where data lives.
- `docs/ARCHITECTURE.md` describes how the code, the data and the background work are arranged, and the size and cost budgets the code must stay within.
- `docs/PROCESS.md` describes how the repository is developed.

## License

Sifty is licensed under the [Elastic License 2.0](LICENSE).
