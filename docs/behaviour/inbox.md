# Inbox, search and the vacancy page

What the user gets: for the chosen profile, the vacancies that fit its search, in four lists (Inbox, Shortlist, Applied, Dismissed) that all search and filter the same way; one decision per vacancy and profile made from the keyboard; and a vacancy page that shows what the vacancy asks and offers, how it fits, and its full text.

## Layout

- The header carries the stage tabs "Inbox N", "Shortlist N" and "Archive N", the profile switcher, the Settings button and, during collection, "Collecting… 5 of 14 sources". Archive counts Applied and Dismissed together.
- The Inbox header also shows "N sources need attention" and the message of a failed collection run (see `collection.md`).
- Archive has two tabs, Applied and Dismissed.
- Opening a profile or Settings replaces the working area. Changing the stage clears the selected vacancy, the held Unread row, the error and the notice; Archive opens on Applied.
- Changing the profile reloads the list for that profile.
- The list width is changed by dragging its edge within limits; a double click resets it; the width is remembered in the browser.
- When the server does not answer, a banner reads "Sifty server is not responding. Retrying…".
- Every message the client shows is of one of two kinds. A notice reporting something done disappears by itself after five seconds; a failure stays until it is dismissed or the action is tried again. Both close with a cross, and Escape closes the newest message on the page. A new message in the same place replaces the one standing there, so messages never stack up. An action the user asked for that fails always says so; a background load or refresh that fails stays silent.
- While a local model loads, a strip reads "Loading model <name> · m:ss", or "Model load failed: …".

## Which vacancies a profile sees

- **Expired vacancies** (past their `expires_at` date) are left out of Inbox, Shortlist and Dismissed and their counters. Applied is not affected: a vacancy the user applied to is shown even if its listing has since expired.
- **Inbox** shows the vacancies with no decision in this profile that fit its search:
  - **Target roles and keywords.** When the profile has both, a vacancy fits when a target role is in its title and a keyword is in its title, tags or text. When it has only one of the two, only that one applies. A profile with neither sees an empty Inbox. Terms match whole words and phrases, ignoring case and punctuation.
  - **All of these.** Every term listed under "All of these" must be in the vacancy's title, tags or text. Terms match whole words and phrases, ignoring case and punctuation.
- **Shortlist, Applied and Dismissed** show every vacancy the profile decided that way, whether or not it still fits the search, so a decision never disappears when the search changes or a vacancy's text is removed.
- **Exclusions** hide a matching vacancy in every list (see below).
- Counters count the same sets.

## Exclusions

There is one list of exclusions, managed in Settings.

- An exclusion is a **word** or a **company**, and it applies to **all profiles** or to **one profile**.
- A word matches as a case-insensitive substring of the title or the company. A company matches the exact company name after normalisation. A company exclusion also matches a vacancy whose apply address points to that company's Greenhouse board.
- A matching vacancy is hidden everywhere: in every list, in counters and in search. An exclusion that applies to all profiles also stops the vacancy's assessment and the fetching of its page (see `collection.md`).
- While a word is typed, the form shows "Would hide N roles".
- "Block company" on a vacancy adds a company exclusion for all profiles. It is offered only when the role names an employer, which a role stored before employers became required may not.
- The "Except" field on a profile page shows and edits the word exclusions of that profile in the same list.
- Removing an exclusion shows the hidden vacancies again at once.

## The list

**A row shows:**

- the company;
- in Applied, ◷ "Listing expired" when the vacancy is past its expiry date; ✓ "Shortlisted" for a vacancy shortlisted from Inbox in this session;
- in every list, Fits you and the requirements counter (see `assessment.md`);
- the title;
- the location: as stored when it has no more than three entries separated by " · ", otherwise the first two and "+N more" ("Remote · AD +188 more"), with the full text on hover when shortened; "Location unstated" when empty;
- in Applied, "· Applied <date>"; in the other lists, "Applied <date> from <profile>" when the user applied to this vacancy from any profile (a deleted profile is named by its last name);
- the publication line: "Published <date> · <age>", "Source date … (unverified)" or "Publication date not reported".

**Read and unread.** An unread row is bold. Opening it marks it read for all profiles. With the Unread filter on, the row stays visible until the user selects another one ("Opening marks a role read. It stays here until you choose another role.").

**Decisions in this session.** A vacancy shortlisted or dismissed from Inbox stays in the list until reload, shown as decided, and can be undone. Under the list: "N dismissed this session — they disappear next time." The selection moves to the next undecided vacancy, wrapping around.

**When a vacancy leaves the open list** — moved to another stage, deleted, or hidden by blocking its company — the selection moves to the next vacancy below it, or to the one above when it was the last.

**The working area always shows a vacancy while the list holds one.** On start, and whenever the list changes stage, profile, query or filters and no vacancy is open, the first vacancy of the list opens by itself; opening it this way does not mark it read, since the user did not choose it. When the list holds nothing, the working area reads "No roles" and "This stage has no visible roles. Change stage or filters, or collect jobs in Sources."; while the list is loading, and while the server does not answer, it shows nothing.

**Empty states:**

- with a query or filters: "No matching roles. Adjust your filters." and a "Reset filters" button;
- Inbox: "Inbox zero. Collect more jobs in Settings → Sources.";
- Shortlist: "Nothing shortlisted yet — press Y in Inbox.";
- Applied: "No applications yet.";
- Dismissed: "No dismissed roles."

**Freshness.** The open list picks up new vacancies within 10 seconds. After a decision only that vacancy is reloaded. A reload never undoes on screen a decision the user has just made.

## Search, filters and sort

- **Search** ("Search company, role, tech", key `/`) finds a case-insensitive substring in the title, company, tags and text; in the text, punctuation counts as a space. A vacancy whose text was removed is found by its title, company and tags only. A query shorter than 3 characters matches title, company and tags only.
- **Filters**, in a panel with a count of active filters:
  - Fits you: Strong 75+, Partial 55–74, Weak, Not scored, without counts; a dimmed result counts as Not scored;
  - Published: All, 7 days or 30 days, by the vacancy's publication date; a vacancy with no publication date passes every choice;
  - Role: Remote (remote, anywhere or telecommute in the location); Leadership title (CTO, Head, Director, Manager, Lead, not "lead generation");
  - Sources: one checkbox per source with its count;
  - Unread (not in Archive);
  - "Showing N / M roles" and "Reset filters", which clears filters, query and Unread and sets the sort to Best fit.
- Search and filters work the same in all four lists, Dismissed included.
- **Sort** Best fit or Newest, as defined in `assessment.md`, in Inbox and Shortlist. Applied and Dismissed are sorted by the date of the decision, newest first.
- The query, the filters and the sort are remembered in the browser.
- **The list arrives in windows.** A request names a window with `limit`, a whole number from 1 to 200 (100 when left out), and `offset`, a whole number from 0 (0 when left out); the rows are that part of the chosen order, so the next window continues where the last one ended. A `limit` or an `offset` that is not a whole number in range is refused with a sentence and nothing is listed. An `offset` past the end gives no rows and is not an error.
- **`matching`** is the number of roles the query and the filters select, counted whole and the same for every window; `total` stays the number of roles in the stage, which the stage tabs show. The stage counts, the counts beside the Sources filter and the empty text are counted over the whole selection, never over the window, so moving to another window changes none of them.

## Stages and decisions

A vacancy has one stage per profile: Inbox (no decision yet), Shortlist, Applied or Dismissed.

| Stage | Actions |
|---|---|
| Inbox | "Block company", "✕ Dismiss N", "✓ Select Y". After a decision in this session: "Moved to Shortlist" or "Dismissed — hidden after this session", with Undo. |
| Shortlist | "Move to Inbox", "Apply ↗ A", "✓ Confirm application…", "× Delete role…" |
| Applied | "Applied <date>", Undo (back to Shortlist), "Delete role…" |
| Dismissed | "Dismissed — <reason>", "Restore to Inbox" (the reason is cleared), "Delete role…" |

**Apply** opens the apply address, or the original address when there is none, in a window beside the current one; only http(s) addresses without credentials are opened. The Apply button shows where it leads: "via employer site", "search for employer site" or "searching…" (see `application.md`). Messages:

- no address: "No valid employer or original listing URL is available. The role stays in Shortlist.";
- window blocked: "The application page was blocked. Allow popups and try Apply again. The role stays in Shortlist."

Then the dialog "Did you submit your application?" asks for the outcome: Not yet; No, vacancy is stale; No, vacancy is closed; No, not suitable; Yes, applied. Escape does not close it. When saving fails: "The outcome could not be saved…".

**The application is a separate fact.** "Yes, applied" records that the user applied, on which date and from which profile. Undo in Applied moves the vacancy back to Shortlist and keeps this fact. Every profile shows "Applied <date> from <profile>" on the row and on the vacancy page.

**Rules the server keeps:**

- moving to the same stage does nothing;
- a reason is accepted only for Dismissed and only from Shortlist; otherwise "the role is no longer awaiting an application decision; reload and try again";
- Applied is reached only from Shortlist and only with the confirmation;
- a decision on a vacancy whose stage changed in the meantime is refused with "job N changed while this outcome was being saved; reload and try again".

**Entering Shortlist** starts, in the background, the Shortlist check (see `assessment.md`), the search for the employer's site and the preparation of the application (see `application.md`).

**Delete** removes a vacancy for good (see `collection.md` for how collection keeps it away).

- It is not offered in Inbox: "Dismiss roles in Inbox; permanent Delete is available outside Inbox".
- It is refused while a collection run is in progress ("Collection is running; try again when it ends.") or while a model call for this vacancy is running ("A model is working on this role; try again when it ends.").
- The dialog heading is "Delete &lt;title&gt;?" followed by the company name, then "This permanently deletes this role… Automatic collection will not restore it." and "Keep role" / "Permanently delete".
- It removes the vacancy's decisions, drafts and pending work.

## Keyboard

- J or ↓ and K or ↑ select the next and previous vacancy, wrapping around.
- `/` focuses search. Escape closes the filters and leaves the field.
- Y selects and N dismisses in Inbox; A applies in Shortlist. All three keys work by physical key position, so they also work on a non-Latin keyboard layout.
- Keys do nothing while a profile, Settings or a dialog is open, while a modifier is held, during IME input, or while a text field has focus.

## The vacancy page

**Header:**

- the company;
- the title;
- "Listing expired", the publication date, each source by name as a link to the source address (hover shows "Found on <name>"), "Original listing ↗", and "Applied <date> from <profile>" when there is an application;
- the actions of the current stage.

**Tabs** Overview and Application. Application is unavailable in Inbox with the hint "Shortlist this role to open Application".

**Overview, from top to bottom:**

1. The Fits you band (see `assessment.md`).
2. What they ask on the left (see `assessment.md`) and What they offer on the right. What they offer shows a "Base salary" tile, carrying the value of the band's Money tile and left out when the vacancy states no money, then tiles built from the vacancy's compensation and benefits sections and an "Also offered" list; when nothing is found: "No Compensation or Benefits section recognised…" or "Section found but no specific items matched."
3. Listing text, with its state in words: "Full text", "Listing summary only", "Blocked by site", "Not fetched" or "Text removed after 60 days unseen; it returns if the source shows the role again". The sanitised text is shown with its headings; without text: "A full description has not been retrieved…".
4. The Shortlist check, in Shortlist and Archive when a check exists (see `assessment.md`).

Opening a vacancy never makes a network request.

## Not carried over

- Search in the browser over all vacancy texts sent with the list.
- Global search expressions and their unreachable editor, with the collection fallback that used them.
- Four separate ways to hide a vacancy: global stop words, blocked companies hidden only in Inbox, profile except-words kept apart from the rest, and triage rules that were never finished. The one list of exclusions replaces them.
- Dismissed without search and filters, and the Archive counter that ignored Dismissed.
- The second Fits you and the second Assess button on the page, raw JSON dumps, file paths, and the "AI analysis" note.
- The "Candidate facts and corrections" form on the page; Confirmed by you holds these facts (see `profiles.md`).
- The internal text state ("available cleaned html") shown on the page.
- The "By section" and "Original" switch of the listing text.
- The automatic refetch of a vacancy when it is opened.
- The text "Applications are permanently deleted 30 days after…", which was never true.
- Erasing the application date when Applied is undone.
- The Web3 mark and the Web3 / blockchain filter.
- The "AI assessment" filter.
- The full history of stage changes, which the interface never showed.

## Data from the old application

Stages are imported per profile: no decision stays Inbox; shortlisted and saved become Shortlist; rejected becomes Dismissed with its reason; applied becomes Applied; the date of the last change becomes the date of the decision. The old per-vacancy copy of the first profile's stage is not imported; it is compared with the first profile's stages first and every difference is logged. Every old application date becomes an application fact; its profile is taken from the last recorded move to Applied, or the first profile when none is recorded; the history of stage changes is read for this and not imported. The read mark is imported as it is. Global stop words become word exclusions for all profiles, blocked companies become company exclusions for all profiles, and profile except-words become word exclusions of that profile.

Before switch-over, for every profile and every list, the vacancy ids and the counters of the old and new lists are compared (see `settings-and-data.md` for the method). The expected differences are listed beforehand: Dismissed now counted in Archive; applications visible in every profile; blocked companies now hidden in Shortlist and Archive too; merged duplicates keeping the strongest stage in every profile, not only the first; decided vacancies staying in their list when the search no longer fits them; Greenhouse vacancies showing the company name instead of the board's name; target roles and keywords each required when a profile has both, so vacancies matching only one are not in the new Inbox; keywords no longer matched in the company name; vacancies whose company cannot be found are not imported, so the previous stop word 'unknown' has nothing left to hide. Fifty vacancies are checked by hand for stage, reason and application date.
