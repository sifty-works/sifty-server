# Profiles and My data

What the user gets: one set of facts about themselves (My data), several profiles that each describe one kind of job they look for, and documents (CVs, notes, pages) from which a model proposes facts that the user accepts or rejects. Only accepted facts are used as facts; the text of a document marked "Use as evidence" is passed to a model as text, never as facts. Saving a change recomputes only what that change affects, in the background and without a model call.

## Who owns which field

- **My data**, one for all profiles: display name, legal name, contacts, photo, application form answers, skills, skills the user does not have, Confirmed by you, experience, languages, availability, current location, work authorizations.
- **A profile**: name, headline, target roles, search keywords and "All of these" terms, compensation, the engagement, working time, work mode and locations lists, weekly hours, relocation, time zones, other requirements.
- Exclusions belong to the one list of exclusions (see `inbox.md`); a profile page shows the word exclusions of that profile.
- An empty field means unknown, never zero or false.
- A save changes the fields it names and no others. A field left empty in the editor is saved as empty, whether the editor sends an empty value or none; only a field the save does not name keeps what was stored. The one exception is the profile name: an empty name is ignored (see Profile switcher).

## Profile switcher

- The header button shows a user icon in the profile's colour and its name, "Profile 1" by default. The colour comes from a fixed palette by the profile's position in the list.
- The "Profiles" menu lists every profile with its colour dot and name, and a check at the active one. Clicking the active profile opens its page; clicking another makes it active and reloads the lists, with no model call.
- "Rename" edits the name in place: Save or Enter keeps it, Escape cancels, an empty name is ignored.
- "+ Add new profile" asks for a name (Create is disabled while it is empty). The new profile gets the next colour, becomes active and opens. It starts empty: no decisions are copied, and its empty goals are inherited from the first profile.
- "Delete profile…" asks for confirmation and removes the profile with its decisions and its exclusions. Applications made from it stay recorded and keep showing its last name. The first profile cannot be deleted, because other profiles inherit from it.
- A click outside the menu or Escape closes it.

## Profile page header

- **Photo.** Clicking the avatar uploads a png, jpg or webp image of at most 2 MiB; the server checks the real image type. "Remove photo" deletes it. Without a photo the avatar shows the two initials of the display name, or an icon. The caption reads "Offered when an application form asks for a photo" or "No photo · click the avatar to add one…".
- **Name.** The title is the display name ("Your profile" when empty). Editing shows Display name and Legal name side by side with "Legal name is optional; used only where a form asks for it…"; the legal name is shown as "Legal name: X ·".
- **Headline**, per profile, at most 160 characters on one line; Enter saves and Escape cancels. When empty, a line "role · company · city, country" is built from the current or first role of the experience, or "Candidate profile".
- "Close" returns to the lists.

## Tabs

Files (with a badge counting suggestions to review), Profile (opened by default), and Questions & Answers.

## Profile tab: Search setup

**What you're looking for:** Roles, Words, All of these, Except. Each is a tag input: Enter, comma, Tab or Add adds a tag; Backspace removes the last one; a pasted list is split; a duplicate ignoring case is skipped. Except shows and edits the word exclusions of this profile in the one list of exclusions. How these fields decide what enters the profile's Inbox is in `inbox.md`. A change applies at once; no vacancy is deleted by it.

**Compensation:** minimum and desired amount; currency (EUR or USD); before or after taxes; period (month or year); payment method (fiat only, crypto accepted, crypto only; crypto accepted by default); interest in equity or tokens in addition to base pay; day rate for contracts (minimum, target, currency; always per day). Amounts are above zero, the desired amount is not below the minimum, and a currency is required with any amount.

**Engagement, working time, work modes and locations:** each is four lists, Preferred, Possible, Unwanted and Never, and an option stands in at most one of them (a second list is refused); an option in none is neutral and does not count for or against a vacancy. The options are engagement (contract, employment, internship), working time (full-time, part-time), work modes (remote, hybrid, onsite) and places (regions and countries chosen from suggestions, cities typed as text). For part-time there are also desired hours per week and an optional minimum and maximum (maximum not below minimum); they apply to part-time vacancies. On the profile page they form one Goals table, a row per criterion and a column per list plus Neutral; a value moves by drag or by a click menu, a place is typed into a column and removed with ×, and every change is saved at once, without an edit mode; changing a row inherited from the first profile makes it the profile's own. How the lists decide Fits you is in `assessment.md`. Profiles saved before the lists existed were converted once: listed work modes became Preferred and the others Never (Unwanted for a profile that was willing to relocate), listed engagement and working time became Preferred and the others Unwanted, and preferred locations became Preferred; a field that was empty stayed empty.

**Geography and authorization:**

- Current location (My data): a country chosen from suggestions and a city typed as text.
- Work locations (profile): the four lists above, with regions and countries chosen from suggestions and cities typed as text.
- Work authorization (My data): by country or region, offered with their size ("EU — European Union (27)"), each eligible or needing a visa.
- Willing to relocate: not set, yes or no; read by the Shortlist check, and Fits you does not use it.
- Desired time zones: from six fixed UTC bands.

**Availability** (My data): notice as a number and a unit (day, week, month) and an available-from date.

**Other requirements:** plain text with Markdown, read by the Shortlist check and by application preparation, not by Fits you ("Used by the Shortlist check and preparation; Fits you does not interpret this text").

**Inheritance.** An empty compensation field, weekly hours, or an engagement, working time, work mode or locations criterion whose four lists are all empty, of a profile other than the first, is shown dimmed with "from <first profile>" and is taken from the first profile when assessing and searching, a criterion as a whole and not list by list. Editing it stores the profile's own value; saving an inherited value unchanged keeps it inherited. A change in the first profile recomputes the profiles that inherit it. Target roles, keywords, time zones, other requirements and the headline are never inherited.

## Profile tab: Personal details

**Skills.** Chips coloured by level with a legend (Beginner, Proficient, Expert); the tooltip shows the level and where the skill came from. Editing shows rows of name and level, "+ Add skill" and ×. "Copy all skills to search" adds the skill names to the active profile's Words, skipping duplicates. Errors: "Skill N: enter a name", ""X" is both in your skills and among the skills you do not have", and a duplicate name.

**Skills you do not have**: chips with ×.

**Confirmed by you**: "You have: …" and "You do not have: …" chips marked ✓ or ✕, and free-text facts, each with ×; "+ Add a fact" adds a free-text fact. "You have" and "You do not have" decide requirements (see `assessment.md`); free-text facts are read only by the Shortlist check and application preparation. The vacancy page also writes here: + on a requirement offers a choice when the requirement names alternatives ("Go or Rust", "A/B"), adds known skills at level Proficient, splitting a list, and otherwise adds "You have: …"; − adds skills the user does not have or "You do not have: …".

**Experience:** company, role, start and end month and year, "Currently working here" (which disables the end), a Markdown summary, "Remove this role" and "+ Add role". Read mode shows "title · company" and dates ending in "– Present".

**Languages:** name and level (Pre-A1, A1 to C2, Native).

**Application form answers:** gender (with self-description), race (several values; "prefer not to say" excludes the others; with self-description), ethnicity, disability, veteran status, criminal record. They are used only to fill application forms. Every option shows its label.

## Files tab

**Files** ("CVs and notes. Nothing is analyzed until you choose Analyze."):

- "+ Add files" accepts several pdf, md or txt files of at most 2 MiB each, also by drag and drop. A file of another kind, a Word document among them, is refused with "The file must be a PDF, Markdown or plain text file." "Paste text instead" takes a file name and text.
- A document card shows its name, its state, "kind · N characters" or a summary of its facts, an error with its cause and what to do ("Open Settings" when no provider is configured) with "Technical details", and the actions Analyze, Cancel analysis, Retry analysis, View or Hide text, and Original. The text of a PDF is read by the program `pdftotext` (poppler) when it is installed on this machine, and then the PDF is like any other file. When it is not installed, the card shows that the text is unavailable, with a reason that names the install command for this operating system (for example "brew install poppler"); Analyze and View text are not available until the PDF has been read, and Refresh reads the stored PDF again, so a PDF added before the program was installed needs no new upload. When the program fails, runs too long (more than 30 seconds), writes more than 4 MiB or finds no text (a scanned page), the card shows an error with its reason and no text.
- "Cancel analysis" is offered while a document is analysing and stops it at once. A queued analysis never starts; a running one is stopped where it is. The document goes back to "ready" with the reason "Analysis cancelled.", and the suggestions already waiting for review stay. The chunks of text already read are kept, so Analyze afterwards continues from the chunk that was reached instead of reading the text again, and a restart does not start a cancelled analysis. Cancelling a document that is not analysing changes nothing and answers "This document is not being analysed."
- A document that was being analysed when the server stopped is shown as "ready" with the reason "Analysis was interrupted. Press Analyze to continue." after the next start; the chunks already read are kept, so Analyze continues from the chunk that was reached, as after Cancel.
- "Use as evidence" marks a document whose text the Shortlist check and application preparation may read.
- Deleting asks "Suggestions from it are removed; facts you already accepted stay in your profile."
- "N of M uploads failed. Successful files are kept." with a Refresh button.

**Contacts and links:** email, phone, LinkedIn, GitHub, X, website. "Fetched pages" are documents made from links ("+ Add link"), with delete, Retry analysis and "Refresh source". The page is requested at the address as typed, and its text becomes the document. Errors, each in its own words: "Enter a URL.", an address that does not start with http:// or https://, a private address, a page that cannot be read automatically (one message covers a page behind a login and a page that refuses automatic reading, because a site does not always say which; a site that answers with an access refusal is reported this way, and one that does not answer is reported as not loaded), a page that was not found, a page that could not be loaded, offline mode, an address that is not a web page, and a page without text. No document is made when the link fails. Adding the same address again returns the existing document; two addresses are the same when they differ only by tracking parameters (`utm_*`, `ref`, `source`, `gh_src`), a leading `www.`, a trailing slash, the letter case of the host or a fragment. Adding or refreshing a link makes no model call. "Refresh source" reads the page again: when the text is unchanged nothing is written, and when it changed the text is replaced, the document goes back to "ready" and the next Analyze starts from the beginning of the new text; when the page cannot be read the document stays as it was, and a document being analysed is not refreshed. Pasted text and md or txt files have no source to read again, and "Refresh source" tells so.

**Suggestions** ("✦ AI N to review" or "Nothing to review"):

- A card shows the field, the proposed value, the quote and the document ("(file deleted)" when gone), with Accept and Reject.
- A conflict shows Current against Proposed, with "Keep current" and "Use proposed".
- An invalid compensation offers "Edit compensation".
- Accepted and rejected cards fade out. A value the profile already has is not proposed.
- Personal facts (skills, experience, languages) go to My data; target facts (roles, compensation) go to the active profile.
- Accept answers at once; the recomputation runs in the background.

## Questions & Answers tab

A list of questions with their answers, shared by all profiles: Edit, Delete and "Add an answer". Application preparation writes to the same list (see `application.md`).

## Recomputation and model calls

- A change that enters Fits you (goals, skills, skills the user does not have, Confirmed by you, experience, languages, location, authorizations) recomputes Fits you of the affected profiles in the background, by code (see `assessment.md`). A change of search fields changes only what the profile sees. A change of name, headline, photo, contacts, application form answers, availability or other requirements changes no score.
- **Analyze or Retry** on a document is the only model call of this area. It uses the provider chosen for reading in Settings, one call per chunk of the document, at most 100 facts, with a timeout of 120 seconds. A fact's quote must match the document text exactly. Headline and application form answers are never proposed.
- What a model receives in the Shortlist check and in application preparation: the accepted data of My data and the profile, and the text of documents marked "Use as evidence". Contacts, legal name and application form answers are never sent to a model.

## Not carried over

- The "Quick ranking up to date" block, the "Refresh scoring" button and the "Candidate context is not current" notice.
- The Settings › Candidate context screen and stored snapshots of candidate context.
- The candidate configuration file: its review notes and source roles are dropped, and its personal site, LinkedIn and GitHub become link documents.
- Archiving a profile; deleting with confirmation replaces it.
- A stored profile colour that the interface never read, and the one-time move of a profile name from browser storage.
- Rich text in experience and other requirements; Markdown replaces it.
- The legacy skill levels and legacy text fields (location, eligibility, relocation, dealbreakers, work-mode limits) kept "until replaced"; they are converted once.
- The "Candidate facts and corrections" field on the vacancy page and its "this role only" scope; Confirmed by you replaces it.
- The desired overlap hours field, which had no input and no reader.
- The hint "Skill names are quick-ranking search signals", which was not true.
- The revision history of My data and profiles, which nothing read.
- Recomputation of every score after any save, whatever field changed.
