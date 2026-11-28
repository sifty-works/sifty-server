# Collection

What the user gets from collection: vacancies from the sites they chose appear in the Inbox without any manual work, each once, with clean readable text, and nothing they removed comes back. Collection makes no model calls and no paid calls.

## Sources

A source is a name, a public http(s) address and an enabled switch, on by default. The user manages sources on the Sources page: a table of name, address, enabled switch and a remove action, and a form to add a source. Removing a source has no confirmation; the source can be added again. The address is required; the form shows the server's error text when the address is not accepted. The form clears on success. The address of an existing source cannot be edited; the user removes the source and adds it again.

Collection visits the enabled sources. Each source is recognised by its address. The description of known sites — which module reads each, its list address, content selector and link pattern, and how its application form is read — is embedded in the application; the user may override or extend it by placing a `sites.yaml` in `<data>/vocab/`. An entry in the override file replaces the embedded entry of the same host or adds a new host; the other embedded entries stay. See `settings-and-data.md` for what happens when the file fails to load.

| Kind of source | How it is read | Requests per run |
|---|---|---|
| Greenhouse, Lever, Ashby boards | the board's public JSON API, which includes descriptions | 1 |
| RSS or Atom feeds that carry descriptions (We Work Remotely, Remotive, Cryptocurrency Jobs); We Work Remotely and Remotive also carry employment type, location, and expiry date in named feed fields | the feed | 1 |
| Remote3 | its feed for the list, then the page of each new vacancy | 1 + new vacancies |
| web3.career, whose own structured data is broken | links on the listing page, then the page of each new vacancy | 1 + new vacancies |
| Framework Ventures (a Getro board) | the jobs page at its list address | 1 + new vacancies without a description |
| a16z Frontier | a script variable on the listing page holding an array of companies and their jobs; each record gives salary, work mode and locations | 1 + new vacancies without a description |
| Paradigm | a SvelteKit inline script holding a JavaScript object literal with job records for portfolio companies; each record gives salary, work mode and locations; records with URLs on other hosts are listed | 1 + new vacancies without a description |
| Braintrust (`www.usebraintrust.com` or `app.usebraintrust.com`, any path) | the first page of its public jobs API for the list, then the API record of each new vacancy for the description and requirements; each record gives company, location, salary (per hour, month or year by the payment type; a fixed-price project gives no salary) and employment type; the person who created a job is never read | 1 + new vacancies without a description |
| Hacker News "Who is hiring?" | three Algolia API requests: one search, then the top-level comments of the two newest "Who is hiring?" stories as postings, so the previous month is read again; each pipe-separated first-line part after the company (the first part) is read as an address (skipped), salary, work mode and employment type (a part made only of such words; a parenthetical is the location), the role (the first part that names a role, even with a work mode in it), a work mode with its place (a part that starts with a work mode, names one in a parenthetical or gives a schedule in days), or the location; a header that names no role gets the title "Open roles"; the newest thread must be read, and a failure to read the older one is skipped | 3 |
| Any other site | JSON-LD `JobPosting` on the listing page; if the answer is XML, RSS or Atom; otherwise links that look like vacancies and Getro `__NEXT_DATA__` | 1 + new vacancies without a description |

Only the first listing page is read; there is no pagination. Sites behind a login are not collected.

The company of a Greenhouse vacancy is the company name the API gives, not the board's name in the address. When a stored Greenhouse vacancy's company changes this way, a company exclusion that named the board also covers the new name.

## When collection runs

- Collection runs on an interval, by default every 3 hours, measured from the start of the last run of any kind. The interval is at least 15 minutes and at most 168 hours (7 days); the Sources page offers the choices 15 minutes, 30 minutes, 1, 3, 6 and 12 hours, 1 day and 7 days, and shows the current value when it is another one. The Sources page carries the switch that turns automatic collection on or off, the interval and the age limit.
- The age limit, "Do not collect roles published more than N months ago", is a whole number of months from 1 to 1200; empty means no limit, which is how an instance starts, and in the settings file no limit is written by leaving the key out. A settings file holding any other value, including 0, is put aside as described in `settings-and-data.md`. A posting whose listing gives a publication date older than the limit is not stored and its page is not requested; it counts in the source's "seen" figure and in no other. A posting with no publication date in its listing is always collected, and a date found only on its own page does not drop it. The limit applies only to postings the catalogue does not hold yet: a vacancy already collected goes on being updated whenever a source shows it, so a new closing date still reaches it. Collection itself changes nothing already in the catalogue. The daily retention run removes the vacancies no profile decided and with no application that the limit puts out of range, and leaves a removal mark for each, so raising the limit or clearing it brings back only the postings the run never removed, the next time a source shows them. With no limit set, retention removes nothing by age.
- Opening or reloading the application starts a run when the interval has passed and automatic collection is on.
- The "Collect jobs" button starts a run at once, ignoring the interval. It is disabled while a run is in progress.
- Only one run happens at a time.
- A run interrupted by a restart of the application is marked interrupted at the next start and never blocks the button.

## Politeness and safety of requests

- Only public http(s) addresses are requested. Private, loopback, link-local, multicast and unspecified addresses are refused before every request and on each of at most 6 redirects. Addresses with credentials are refused. A response body is limited to 8 MB. Every request has a timeout of 30 seconds. The User-Agent is `Sifty/1.0 (personal read-only job discovery)`.
- Requests to one domain are spaced by 3 seconds plus a random pause of up to 3 seconds. Sources on different domains are collected at the same time.
- After errors a domain is retried with an exponential pause from 30 seconds to 1 hour. A domain that answered with a block (403, or a captcha or Cloudflare page) is not requested again for 24 hours; this pause survives the end of the run and a restart.
- A 404 or 5xx answer is an error, never content: its body never becomes a description.
- When a domain blocks during a run, the remaining vacancies of that domain are still saved with the fields from the listing and the text state "Blocked by site".
- A vacancy's own page is requested only when its listing entry has no description and the vacancy is new or stored without text. A known vacancy without text is requested at most once in 7 days, and not while its domain is blocked. A vacancy stored with text is never requested again by collection.
- Within a run, an item whose address was already seen in the same run is skipped before its page is requested.
- An address that matches a removed vacancy, or an item that matches an exclusion, is dropped before its page is requested. Exclusions are checked again on the fields read from the vacancy's own page.

## Fields and text

- Fields kept for a vacancy: title; company, which is required (see below for a posting that has none); original address, canonical; apply address, only when it points to one concrete vacancy in an applicant tracking system (Greenhouse, Lever, Ashby, Workable), never to the root of a board; location; tags; structured fields the source states, such as salary, employment type or workplace type; publication date with its basis, only when the source states a publication date (an update date is not accepted, a future date is dropped, a date once set is not replaced by another); expiry date (from JSON-LD `validThrough` or from a feed's expiry field).
- Ashby, Lever, Getro, a16z and Paradigm boards state salary (formatted as "CURRENCY MIN–MAX/period"), workplace type and all locations when available.
- A vacancy past its expiry date shows "Listing expired" with the ◷ mark and is not hidden. When its expiry date has passed by more than a day and no application has been recorded for it, the daily retention run removes it permanently. When the age limit is set, the same run also removes a vacancy that no profile decided, with no application, published (or, without a publication date, first seen) before the limit (see `settings-and-data.md`).
- Any input text (HTML, escaped HTML, Markdown, plain text) becomes sanitised HTML limited to `h2 h3 h4 p ul ol li strong em br`. A paragraph that reads as a known section heading is shown as a heading.
- A posting that names no company is a reason to request the vacancy's own page, beside a missing text, whether or not the listing gave a text. For a vacancy already stored with a company this is never a reason, so such a vacancy is not requested for it. For a stored vacancy with no company the request follows the same rule as a missing text: at most once in 7 days.
- A posting that still has no company after that attempt is not stored, whether the page named none or could not be read. The run counts it for its source. A posting that an exclusion hides is not stored either, because its page is never requested and its company therefore never found, but it is counted as excluded and not as a posting without an employer: the source did its part. Nothing is remembered about a posting that was never stored: the next run finds it in the listing and tries again. A vacancy stored without a company before employers became required records that its page was asked for, so it is asked again at most once a week like any other. A stored vacancy that has a company is updated as usual when a listing gives none, and keeps its company.
- On a vacancy's own page, JSON-LD is matched to exactly one listing item, by address or by a unique title; a page where several items share the same title is not guessed. When matched, the item fills empty fields (company, location, dates, employment type, salary); its description fills the text unless the site marks descriptions as unreliable, in which case the body is used for text instead. Without a JSON-LD match (and without ambiguity), the sanitised body of the page is used (the content block, for site-specific sources), and the page title in the form "Role at Company" fills an empty company. A link to one concrete vacancy in an applicant tracking system found on an aggregator's page becomes the apply address.
- The vacancy page shows the text state in words: "Full text", "Listing summary only", "Blocked by site", "Not fetched", or "Text removed after 60 days unseen" (see `settings-and-data.md`).

## New, changed, duplicate, removed

- A listing item matches a stored vacancy when (a) its canonical address was seen before, or (b) its apply address is the same concrete vacancy in an applicant tracking system, or (c) company and title, normalised, are the same, the company is not empty, the address is on another host, and the apply addresses do not conflict. Two apply addresses conflict only when they are both concrete vacancies on the same applicant tracking system; addresses on different systems (for example Ashby and Greenhouse) do not conflict, so a role that moved between them matches by company and title.
- **New:** the vacancy is saved with no decision and appears in the Inbox of every profile whose search it fits. The open Inbox picks up new vacancies within 10 seconds.
- **Matched:** the last-seen date is updated. The title is always taken from the newer listing; company, location and tags when the newer value is not empty, and a company only when it was not guessed from the board's address. The text is replaced when it differs from the stored text and the new text is not worse: not empty and not blocked. A vacancy whose text was removed always takes the text again.
- **Several matches:** they are merged into the oldest vacancy. For each profile the strongest stage is kept (Applied over Shortlist over Dismissed over Inbox); the application, drafts and the read mark follow, and the old id keeps pointing to the merged vacancy. Ids of merged and removed vacancies are never reused.
- **Removed for good:** the user removes a vacancy (not from Inbox, with confirmation, not during a run). Each of its addresses is remembered, and collection drops any later item with that address before requesting it.
- **Dismissed:** a dismissed vacancy stays dismissed; collection never brings it back as new. Its text may be removed after 60 days unseen (see `settings-and-data.md`); if the source shows it again, its text is taken again and the decision stays.

## Exclusions

There is one list of exclusions, described in full in `inbox.md`. For collection it means: an item whose title or company matches an exclusion that applies to all profiles is saved with the listing's fields only, its page is not requested, it is not assessed and it is not shown. A company exclusion also matches a vacancy whose apply address points to that company's Greenhouse board. Removing the exclusion shows it again at once; its text is fetched at the next run that sees it.

## What the user sees of progress and errors

- During a run, the Sources page shows "Collecting… 5 of 14 sources" and the status bar the same text with a bar and "5 / 14".
- After a run, the Sources table shows each source's status in a Status column:
  - After a successful run: "Last success 2 Jan 15:04 · 10 seen · 3 new", followed by " · 2 without an employer" when the run did not store some postings for lack of a company.
  - After a failed run: the error in words ("Blocked by site", "Timed out", "Site not answering", "Not found", "Nothing recognised on the page"), followed by " · last success 2 Jan 15:04" when there was a prior success.
  - "No vacancies in 30 days" when the source's first run is at least 30 days old and no run in the last 30 days found a vacancy. This replaces the success form.
- A source whose last three runs (at least three) all ended in error or empty is marked with ⚠ in the Status column.
- The status bar shows "N sources need attention" (or "1 source needs attention") among its messages, opening the Sources page, when any enabled source needs attention.
- A run where every source failed is shown as failed with a message on the Sources page and in the status bar.

## Actions on a vacancy

- "Refresh source" on the vacancy page requests the vacancy's page again and replaces the text by the rule for matched vacancies. Opening a vacancy never makes a network request by itself.

## Not carried over

- The daily run at a fixed hour; the interval and the button replace it.
- Import of a single vacancy by address, and collection of a single source; neither had a way to be used.
- The jobspy source (LinkedIn, Indeed); it never worked.
- Automatic refetch of a vacancy when it is opened.
- A history of changes of a vacancy's text, stored originals of fetched pages, and the notice "vacancy changed; this assessment uses an older snapshot".
- The "Source coverage" page with its counters, and the settings in the advanced settings file that had no effect.
- The Web3 mark as a fixed pattern.
- Sources that yield nothing: measured on a copy of the old data before switch-over, and those with no vacancy are not carried into the new configuration.
