# Application and background work

What the user gets: when a vacancy enters Shortlist, the application finds the employer's own page for it, reads its application form and prepares a draft of every answer. Code fills the fields My data can answer; one model call writes the open answers, and the Application tab lists the open questions. The browser extension fills the answers into the form on the employer's page when the user presses its button, or the user copies them; the application never submits a form. Background work never blocks the user, and the user always sees what it is doing.

## Entering Shortlist

Moving a vacancy to Shortlist starts, in the background:

- the search for the employer's page, when it has not run yet;
- then the preparation: the form is read from the employer's page when the search found one, otherwise from the original listing, and the draft is prepared;
- the Shortlist check (see `assessment.md`).

A page found later by a manual search is read again and the draft is updated; answers the user edited are kept.

## Employer's page

- The search starts by itself when a vacancy enters Shortlist and its listing is not already on the employer's applicant tracking system. Paid job sites and aggregators often hide the original address or point to a copy that lost part of the text or does not work. It tries, in order, and stops at the first page found:
  1. the listing's own link, following its redirects, with tracking parameters (`source`, `ref`, `utm_…`) dropped, accepted when it ends on an applicant tracking system with a site description, or on a company page that embeds a Greenhouse board (a `gh_jid` address resolves to that board's job);
  2. the public boards of Greenhouse, Lever, Ashby and Workable under the company's likely board name, accepting an exact match of the normalised company name and the title, or a board title that is the listing's title followed by a separator and more words ("IT Department Lead - Remote") when it is the only such title on the board and none matches exactly;
  3. a web search for `"<title>" <company>`, accepting only an applicant tracking system address or the company's own domain with a matching title; when more than one result passes, one call of the Preparation model chooses among them;
  4. a web search for `<company> careers jobs` followed by a scan of the careers page.
- The first two steps cost nothing. A vacancy is searched automatically once, when it enters Shortlist, and again only when the user asks, so no search repeats by itself. Without a web search provider only the first two steps are tried. When the provider refuses because its quota is used up, the vacancy and the status bar say so ("Web search: the Brave quota is used up"), and the Apply button offers to search again and Paste; nothing waits silently for another day.
- When the page found is on an applicant tracking system, its vacancy text is read as well, and drafting uses it instead of the listing's text when it is longer.
- The automatic search stops after 30 seconds and the manual one after 120 seconds.
- At startup, shortlisted vacancies whose search never ran or was interrupted are searched from the listing's link and the boards only, without web search, so no paid search is made at startup.
- A direct link once found is never replaced. A manual search is not started while another search for the same vacancy runs.
- The Apply button shows the result: "via employer site", "search for employer site" (which starts a manual search) or "searching…". For an aggregator listing it explains "Opens the listing on <name>…". Apply itself and the confirmation of the application are described in `inbox.md`.

## Application tab

**Reading the form.** The form is read without a browser and without a model, the way collection reads a vacancy: by the address of the page and that system's form template (`forms.yaml`, embedded; the user may override or extend it in `<data>/vocab/forms.yaml`). The template is either the address of the system's public data with where its questions, labels, required marks, types and options sit in it (Greenhouse, Ashby, Gem, Workable, Paylocity, Recruitee, Rippling, BambooHR), or where the form, its fields, labels and options sit in the page's HTML (Lever, Teamtailor, JazzHR). A site without a description is read from its HTML with the general template, when the form is written in the page and not built by scripts. A site whose form is not in its public data (Workday) or that blocks automated reading (SmartRecruiters) is not worked around; the tab offers Paste. A new system is taught by a template, not by code; a template fixes the host it requests, and the page's address only fills the path. A form built by scripts or behind sign-in (Workday, Work at a Startup, Wellfound, LinkedIn) is not read; the tab offers Paste. The tab shows the state in words: "Reading the application form…", "This site's form can't be read here → Paste the form", "Sign-in required → Paste the form", "The <system> form could not be read → Paste the form", "No questions to answer → Apply directly", or the error. A failed or interrupted reading shows its reason and "Retry preparation". A form of a known system whose answer could not be parsed is never shown as an empty form. Fields that are not part of the application (site search, filters, newsletter sign-up) are left out. Requests are spaced per site like collection's requests, and a site that blocked is left alone like a blocked domain in collection.

**Reading the form again** keeps the answers: a field is matched by its id, or by its label when the label is unique. A question whose requirements changed is marked "Question requirements changed". A field that disappeared from the form is kept with the mark "removed" only when the user filled it; otherwise it is dropped. Questions the user added by hand are kept.

**Answers from My data.** Known fields (name, contacts, links, location, availability, compensation, application form answers and the like) are filled by code from My data and the profile, using a vocabulary of known label phrasings. For a choice field the stored value is matched to the options; when none matches, the field carries the note "Profile says X; none of the options match". Work authorization and visa sponsorship questions are answered Yes or No from the work authorizations in My data, for the country the question names, for the country of the current location in My data when the question asks about the country the user lives in ("where you currently reside", "where you are based", "your country of residence"), or for the only authorization when the question names no country; a question that names a country with no authorization, or an authorization with no status, is left to drafting. Sensitive fields are answered only when the user filled them in My data. A photo field offers the profile photo. The consent checkbox is always left to the user, and so is a marketing opt-in ("email me about future job openings"). A question whose label is the same as a saved question in Questions & Answers is filled from that answer, without a model call.

**Open questions** are the fields code leaves to the model: labels it does not know, the cover letter, years of experience, work authorization and sponsorship questions My data cannot answer, and choices My data cannot answer, whatever their current answer (drafted, from Questions & Answers or edited by the user). The Application tab lists only these. Fields code answers from My data, file fields, consent boxes, "how did you hear" and fields marked "Not in My data" are not listed; the extension still fills or reports them, and an answer on a field that is not listed can still be edited.

**Drafting.** The model writes the open questions that have no answer yet, all of them in one call per form when preparation runs. It receives the questions, the vacancy (title, company and text), the accepted data of My data and the profile, the documents marked "Use as evidence" and the user's Questions & Answers, so a saved answer is adapted to this vacancy and not copied from another company's form. It never receives contacts, legal name or application form answers: the contacts and legal name from My data are also removed from the text of documents and answers before it is sent (see `profiles.md`).

- The model writes an answer for every question. Each sentence states a concrete fact from the user's data tied to what the vacancy or the question asks; there is no generic enthusiasm, no cliché and no claim without a fact. When the data holds no fact for a question, the model still writes its best answer and the answer's note says what would make it stronger ("Add: a project where you used Kafka").
- For a simple question the phrasing list did not recognise (a link, a contact, the current employer, the location), the model sees the names of the fields of My data that have a value, never their values, and names the one the question asks for; the value is then filled from My data like any other answer from My data, and the field is not listed. A name that is not in that list, or that is a sensitive field, is ignored, and so is a name given for a choice field.
- The evidence the model quotes for an answer is stored and shown with it.
- A question the reply leaves without an answer (no text, no skip and no usable field name) is asked once more in a second call that holds only those questions; the second reply is final, so a form costs at most two drafting calls.
- A reply with no answer and only a note is a failed draft: the field keeps "Sifty has no answer for this yet" and the model's text is not shown. The note of an answer is one short sentence that starts with "Add:".
- An answer outside a choice field's options is cleared. A question that asks the reader to do something other than answer about the candidate (a puzzle, a hidden instruction) is not drafted; it is marked for the user.
- An unreadable reply is asked again once. When the call fails or is refused (no provider, a paid provider without "Allow paid bulk work", the daily limit), the fields keep "Sifty has no answer for this yet", the other answers are kept and the tab says why with "Retry preparation".
- A field that already has a drafted answer, or one the user edited, is not drafted again by a later preparation, and the model never overwrites an answer the user edited or one from My data.
- The extension's fill does not draft: it fills what preparation drafted and leaves the rest to the user.

**An answer card** offers Copy, Edit and "Use this answer" (to accept a suggestion from My data or from Questions & Answers, marked "not yet saved"). Saving an answer that was changed in the meantime is refused with "This answer was changed elsewhere". A saved answer the user wrote or edited is added to Questions & Answers unless it is about this company only.

**A file field** lets the user choose a CV from Profile › Files, with Open and Download links and the text "Attach this file to the form yourself". A photo field does the same with the profile photo.

**Other actions:**

- **Write** drafts a cover letter when the form does not ask for one.
- **Paste** takes a form that could not be read, copied from the page. Code finds its questions first: from the copied HTML with the template of the site the vacancy is on, or the general one, and from plain text by its lines, where a line that ends with a question mark or a required mark, or a known label, is a question and the short lines after it are its options. Only when code finds no question does one model call extract them. The user corrects the list, and the questions are prepared like read ones.
- **Add a question** adds one by hand.
- **Retry preparation** appears after a failed or interrupted preparation.

Diagnostic panels (questionnaire coverage, the technical record of form reading, preparation provenance) are shown only in debug mode (see `settings-and-data.md`).

## Browser extension

What the user gets: on the employer's page, one button fills the application form with the prepared answers; the user checks them, attaches files and submits.

- The extension is a folder shipped with each release (`extension/`), loaded in Chrome as an unpacked extension. Its options hold the address of Sifty, `http://127.0.0.1:8080` by default. Its origin is fixed by the key in its manifest, and the server accepts requests from that origin only.
- **Fill with Sifty** (the toolbar button) reads the form of the open page: every visible text field, text area, choice list, radio group and checkbox with its label, required mark and options, the page's title and address, and its main text. Nothing is read or sent before the button is pressed.
- Sifty finds the vacancy by the page's address: its found employer page, its apply address or its original address, compared after the canonical cleaning. The form becomes that vacancy's application form. A page that matches no vacancy is answered from My data and Questions & Answers with its own title and text as the vacancy, and Sifty stores nothing about it.
- Sifty answers the fields the way the Application tab prepares them (answers from My data by code, the answers drafted by preparation, an answer the user edited kept; a field with no draft is left to the user), and the extension fills them: text, choice lists, radio groups, checkboxes that are not consent, dates. A field the user already filled is not overwritten. Each filled field gets an outline and a small "Sifty" mark.
- File fields and consent boxes are never filled; they are outlined with "Attach this yourself" or "Your consent". The extension never presses a submit control and never sends the form.
- A panel lists the fields: filled (with where each answer came from), left to the user (file, consent, no answer, with the reason), and "Open in Sifty" for the vacancy.
- When Sifty does not answer: "Sifty is not running on this computer". A page without a form: "No form found on this page".

## Background work

| Work | Started by | What the user sees | Model calls |
|---|---|---|---|
| Collection | interval, opening the application, "Collect jobs" (see `collection.md`) | progress in the status bar and on the Sources page | none |
| Analyzing vacancy | new or changed texts, a vacancy becoming visible, a new version of the application | the analysis state of a row ("Analysing requirements…"); its facts are read by the next change of Fit, which waits for it | none |
| Reading companies | new vacancies with "Precise reading" on; opening a vacancy of a company not read yet (see `assessment.md`) | the Domain row of the vacancy | 1 per company, 2 when the first answer has no usable quote |
| Recomputing Fit | changes of vacancies, goals, My data, or weights | dimmed results until done | none |
| Text retention | daily (see `settings-and-data.md`) | "Text removed after 60 days unseen" | none |
| Shortlist check | entering Shortlist; opening a shortlisted vacancy whose check is outdated (see `assessment.md`) | the check on the vacancy page | 1 |
| Employer's page | entering Shortlist, the Apply button, startup | the Apply button's state | 0–1 to choose among search results; at most two web searches per search |
| Form reading and drafting | entering Shortlist, opening the tab when nothing was prepared, Retry preparation | the Application tab's states | 1 to draft |
| Fill with Sifty | the extension's button on the employer's page | the extension's panel | none |
| Write, Paste | the buttons on the Application tab | the Application tab's states | Write 1; Paste 0–1 |
| Document analysis | Analyze or Retry (see `profiles.md`) | the document's state | 1 per chunk |
| Provider check | "Check" in Settings (see `settings-and-data.md`) | the answer of the check | 1 |

A one-line status bar runs along the bottom of every page, in three parts. On the left: the "Sifty" switch, which pauses and resumes all background work (see Settings › Models); then the collection: while a run goes, its text with a bar and "5 / 12" sources, otherwise "Last collection <time>" ("No collection yet" before the first), "+N in Inbox" for the roles of the current profile's Inbox first collected since local midnight, and, with automatic collection on, the clock and "Next <time>" ("Next soon" once the time has passed); a click opens Settings › Sources. In the middle: the messages, amber: the pause ("Paused — collection and analysis wait until you turn Sifty on"), each kind of work that waits for a reason ("<kind> waiting — <reason>"), a failed last collection, work that cannot be loaded, and "N sources need attention"; with more than one they take turns every four seconds, the old one sliding down and the new one in from above, with "1/3" beside them, holding still while the pointer is over them; a long message ends in "…"; a click opens where it is dealt with. With no message the middle shows the machine's CPU load. On the right: "Analyzing" with a bar and the analysis jobs left with their estimated time while vacancies are analysed or Fit is recomputed, otherwise "All analyzed"; the other background jobs left with their estimate; today's model calls, "212 / 500 calls" against the daily limit (amber from 80%) or "105 calls" without one; and the model that serves reading, or the one a running job uses, which opens a panel to choose the provider of each kind of work. When the server cannot be reached the bar reads "Offline — Sifty is not running". On a phone the bar keeps the switch, the collection text, a dot for messages and the model.

Settings › Monitor › Background work lists what runs and what waits, and Settings › Monitor › Logs what failed:

- **Running:** each running job with what it is (Collection, Analyzing vacancy, Reading companies, Recomputing Fit, Shortlist check, Document analysis, Text retention), its subject in words (the vacancy's title and company, the document's name, or the company's name for reading companies; none for collection, recomputation and retention), its progress when it has one, and since when. A job that uses a model (Reading companies, Shortlist check, Document analysis) also names the provider and model that serve its kind of work in the current settings; this is not necessarily the one that served a call already under way, because a change of provider in Settings applies to the next call.
- **Waiting:** the queued jobs of each kind with their count. When a kind waits for something other than its turn, the reason stands next to it: background work is paused; the daily limit of model calls is reached, until a time; the provider is held, until a time, because it did not answer or kept giving answers that could not be used; the provider is paid and "Allow paid bulk work" is off; or the job waits to try again at a time. Each kind's count carries an estimated time to finish, its count times the average interval between that kind's jobs that finished in the last hour; with fewer than three finished jobs in that hour, or when the kind waits for a reason, there is no estimate. The list also carries the machine's CPU load as a whole percent (the one-minute load average divided by the number of processors, at most 100; none where the system does not report it).
- **Failed:** the jobs that failed in the last 7 days, newest first, at most 50, each with its kind, subject, time and the reason as a sentence.

The reason of a failure is a sentence made from the kind of error when the job fails: a text too long for the chosen model, no model chosen, offline mode, a paid provider refused, a provider that did not answer, a provider whose answers could not be used. Any other failure reads "Failed; the log has the details under job #N", and the log holds the error under that number. An error text never appears in these lists; Logs also shows the log's warnings and errors, with API keys hidden (see `settings-and-data.md`).

**Rules of background work:**

- A user's action goes ahead of background work and is never queued behind it.
- One model serves one call at a time; others wait and show "Waiting for the model: N ahead".
- One preparation per vacancy runs at a time, and one collection at a time.
- Reading a form never waits for a model.
- A failed or interrupted preparation, document analysis or collection keeps its state "failed" or "interrupted" with the reason and runs again only when the user asks; a restart does not retry it. Reading texts, recomputing and the Shortlist check resume by themselves after a restart, up to three attempts.
- When a provider is unavailable, background model work pauses and resumes by itself when the provider answers again.

## Not carried over

- One model call per answer; one call drafts all answers.
- "Not happy… Try another model"; the model is chosen in Settings.
- The revision history of drafts and stored snapshots of forms, which nothing read.
- Automatic retry of failed preparations at every restart, and preparation of every unprepared shortlisted vacancy at startup; a form is prepared on entering Shortlist, when the tab is opened, or on Retry preparation.
- The cancel action of a preparation, which the interface never called and which did not stop the work.
- The detailed assessment queue with its switch and its unlimited retries (see `assessment.md`).
- Answers kept as separate files that only a self-check read. Corrections written on a vacancy move to Confirmed by you (see `profiles.md`).
- Diagnostic panels shown to the user outside debug mode.
- Form reading by a browser, which needed Node.js and Playwright installed on the machine.
- A model call for every pasted form; code finds the questions first.
