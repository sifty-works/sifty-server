# Application and background work

What the user gets: when a vacancy enters Shortlist, the application finds the employer's own page for it, reads its application form and prepares a draft of every answer. Code fills the fields My data can answer; one model call writes the open answers. The user copies the answers into the form; the application never fills or submits a form itself. Background work never blocks the user, and the user always sees what it is doing.

## Entering Shortlist

Moving a vacancy to Shortlist starts, in the background:

- the search for the employer's page, when it has not run yet;
- then the preparation: the form is read from the employer's page when the search found one, otherwise from the original listing, and the draft is prepared;
- the Shortlist check (see `assessment.md`).

A page found later by a manual search is read again and the draft is updated; answers the user edited are kept.

## Employer's page

- The search makes no model call. It tries, in order: the public boards of applicant tracking systems (Greenhouse, Lever, Ashby, Workable) under the company's likely board name, accepting an exact match of the normalised company name; a web search for `"<title>" <company>`, accepting only an applicant tracking system address or the company's own domain with a matching title; a web search for `<company> careers jobs` followed by a scan of the careers page. Without a configured web search provider only the boards are tried (see `settings-and-data.md`).
- The automatic search stops after 30 seconds and the manual one after 120 seconds.
- At startup, shortlisted vacancies whose search never ran or was interrupted are searched on the boards only, without web search, so no paid search is made at startup.
- A direct link once found is never replaced. A manual search is not started while another search for the same vacancy runs.
- The Apply button shows the result: "via employer site", "search for employer site" (which starts a manual search) or "searching…". For an aggregator listing it explains "Opens the listing on <name>…". Apply itself and the confirmation of the application are described in `inbox.md`.

## Application tab

**Reading the form.** The form is read by a browser without a model. The tab shows the state in words: "Reading the application form…", "Sign-in required…", "The site asked to slow down…", "No application form was resolved → Apply via the original listing", "No questions to answer → Apply directly", "Form reading needs Node.js and Playwright on this machine → Paste the form instead", or the error. Fields that are not part of the application (site search, filters, newsletter sign-up) are left out. Requests of the browser are spaced per site like collection's requests, and a site that blocked is left alone like a blocked domain in collection.

**Reading the form again** keeps the answers: a field is matched by its id, or by its label when the label is unique. A question whose requirements changed is marked "Question requirements changed". A field that disappeared from the form is kept with the mark "removed" only when the user filled it; otherwise it is dropped. Questions the user added by hand are kept.

**Answers from My data.** Known fields (name, contacts, links, location, availability, compensation, application form answers and the like) are filled by code from My data and the profile, using a vocabulary of known label phrasings and the labels learned before. For a choice field the stored value is matched to the options; when none matches, the field carries the note "Profile says X; none of the options match". Sensitive fields are answered only when the user filled them in My data. A photo field offers the profile photo. The consent checkbox is always left to the user.

**Labels the code does not know** are classified by one model call per form that sees only the labels, never the user's values. The result is remembered, and the same label never needs a model call again. A label the model classifies as a name or contact field is accepted only when the label itself names it.

**Drafting.** The model writes only the cover letter and the open questions: free-text fields, unrecognised text fields, and choice fields My data cannot answer. All of them are drafted in one model call per form. The model receives the questions, the vacancy, the accepted data of My data and the profile, and the documents marked "Use as evidence"; never contacts, legal name or application form answers (see `profiles.md`).

- Progress reads "Drafting 4 answers", "Waiting for the model: N ahead" or "Loading model".
- A drafting call that gets no answer within 5 minutes fails with "The model did not answer in time". An empty reply is retried once; fields left empty in a reply are reported as failed.
- An answer that cites no evidence from the user's data is cleared with the note "No supporting candidate evidence". An answer outside a choice field's options is cleared. An answer longer than the field allows is marked as too long.
- The result reads "Drafted N of M answer(s); K failed: <reason>". One failed field does not discard the others.
- An answer the user edited is never overwritten by the model or by My data.

**An answer card** offers Copy, Edit and "Use this answer" (to accept a suggestion from My data or from Questions & Answers, marked "not yet saved"). Saving an answer that was changed in the meantime is refused with "This answer was changed elsewhere". A saved answer the user wrote or edited is added to Questions & Answers unless it is about this company only.

**A file field** lets the user choose a CV from Profile › Files, with Open and Download links and the text "Attach this file to the form yourself". A photo field does the same with the profile photo.

**Other actions:**

- **Write** drafts a cover letter when the form does not ask for one.
- **Paste** takes the text of a form the browser could not read; the model extracts its questions, and they are prepared like read ones.
- **Add a question** adds one by hand.
- **Retry preparation** appears after a failed or interrupted preparation.

Diagnostic panels (questionnaire coverage, the technical record of form reading, preparation provenance) are shown only in debug mode (see `settings-and-data.md`).

## Background work

| Work | Started by | What the user sees | Model calls |
|---|---|---|---|
| Collection | interval, opening the application, "Collect jobs" (see `collection.md`) | progress in the status bar and on the Sources page | none |
| Analyzing vacancy | new or changed texts, a vacancy becoming visible, a new version of the application | the analysis state of a row ("Analysing requirements…"); its facts are read by the next change of Fit, which waits for it | none |
| Recomputing Fit | changes of vacancies, goals, My data, or weights | dimmed results until done | none |
| Text retention | daily (see `settings-and-data.md`) | "Text removed after 60 days unseen" | none |
| Shortlist check | entering Shortlist; opening a shortlisted vacancy whose check is outdated (see `assessment.md`) | the check on the vacancy page | 1 |
| Employer's page | entering Shortlist, the Apply button, startup | the Apply button's state | none; web search |
| Form reading and drafting | entering Shortlist, opening the tab when nothing was prepared, Retry preparation | the Application tab's states | 0–1 to classify labels, 1 to draft |
| Write, Paste | the buttons on the Application tab | the Application tab's states | 1 each |
| Document analysis | Analyze or Retry (see `profiles.md`) | the document's state | 1 per chunk |
| Provider check | "Check" in Settings (see `settings-and-data.md`) | the answer of the check | 1 |

A one-line status bar along the bottom of every page shows the state as a dot (grey when idle, blue while a job runs, amber when work waits or the last collection failed, red when the server is offline), a title and a detail. While a job runs the title is the job's label and the detail is its subject and, for a job that uses a model, the provider and model, with "+N more" when several jobs run; a job that reports progress, and collection, show a bar with "5 of 12". When work waits for something other than its turn the title is "<kind> paused" if the reason is a pause and "<kind> waiting" for any other reason, and the detail is the reason; when the last collection run failed the title is "Last collection failed" and the detail is the run's message; while a job runs, the reason and the failure message are added to its detail. With nothing running the title is "Idle" and the detail reads "Last collection <time>" ("No collection yet" before the first); when the work cannot be loaded the title is "Background work could not be loaded", and when the server is offline it is "Offline". On the right the bar shows the waiting count of each kind (a kind that waits for something other than its turn is marked) and "Details ›". Clicking the bar opens Settings › Monitor › Background work.

Settings › Monitor › Background work lists what runs and what waits, and Settings › Monitor › Logs what failed:

- **Running:** each running job with what it is (Collection, Analyzing vacancy, Recomputing Fit, Shortlist check, Document analysis, Text retention), its subject in words (the vacancy's title and company, or the document's name; none for collection, recomputation and retention), its progress when it has one, and since when. A job that uses a model (Shortlist check, Document analysis) also names the provider and model that serve its kind of work in the current settings; this is not necessarily the one that served a call already under way, because a change of provider in Settings applies to the next call.
- **Waiting:** the queued jobs of each kind with their count. When a kind waits for something other than its turn, the reason stands next to it: background model work is paused; the daily limit of model calls is reached, until a time; the provider is held, until a time, because it did not answer or kept giving answers that could not be used; the provider is paid and "Allow paid bulk work" is off; or the job waits to try again at a time.
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
