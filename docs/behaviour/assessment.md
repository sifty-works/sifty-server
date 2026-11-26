# Assessment

What the user gets from assessment: every vacancy in their lists carries one number, Fits you, that says how well the vacancy fits their goals, and a counter of its requirements that says how well they fit the vacancy. Both are computed by code from the user's own data and never hide a vacancy; after a correction the open vacancy changes at once and the others follow in the background. Vacancies are read by code; a model is used only for one check of a shortlisted vacancy.

## What the user states

**Goals, per profile:** target roles; minimum compensation with currency, period and tax basis; engagement; hours; work modes; preferred locations; time zones, chosen from six fixed UTC bands; other requirements, read by the Shortlist check and not by Fits you. An empty compensation, engagement, hours, work modes or preferred locations field of a profile other than the first is taken from the first profile when assessing (see `profiles.md`).

**My data, shared by all profiles:** skills with a level; skills the user does not have; "Confirmed by you", a list of "You have: …" and "You do not have: …" statements and of free-text facts about the user; experience, from which code derives total years, years in leadership roles, years in engineering roles, founder experience and work in the last year; languages; current location; work authorizations by country or by region, each eligible or needing a visa, where a region covers its countries (the EU covers its member states).

## Fits you

Fits you is a number from 0 to 100 and the only number the user sees. It is a weighted average of the criteria below, rounded; a criterion that cannot be compared counts as 0.5, and a criterion with weight 0 is left out of the number and the tiles. A profile with no goals gives no number: goals are target roles, a minimum compensation, engagement, hours, work modes, preferred locations and time zones; work authorizations alone are not goals. A vacancy whose full description is unavailable gives no number.

| Criterion | Starting weight | How it is compared |
|---|---|---|
| Role | 0.25 | best word overlap of the vacancy title with a target role |
| Money | 0.20 | `min(1, top of the vacancy range / user's minimum)` after conversion |
| Location | 0.075 | see Location below |
| Remote restriction | 0.075 | see Location below |
| Time zone | 0.10 | 1 when the vacancy and the user share a band |
| Work mode | 0.04 | 1 when the vacancy offers one of the user's work modes |
| Engagement | 0.03 | employment or contract against the user's goal; a vacancy that states no engagement is treated as employment |
| Hours | 0.03 | full-time or part-time against the user's goal, and for part-time the weekly hours when the user states them; ranges that do not overlap are a mismatch |
| Onsite work authorization | set from data | see Location below |
| Requirements | set from data | the requirements score below |

Weights live in configuration. The weights of onsite work authorization and of requirements are chosen by measurement on hand-labelled vacancies before the first release.

**Money.** The vacancy's range is converted to the user's currency by rates from configuration. A sum without a tax note is gross. A sum without a period is yearly; a monthly sum is multiplied by 12, a daily sum by 260, an hourly sum by 2080. Money cannot be compared when the vacancy states no top of its range or no currency, when a currency has no rate, or when the vacancy's tax basis differs from the user's.

**Location.** The Location criterion compares the vacancy's places with the profile's preferred locations; without preferred locations it cannot be compared. A vacancy matches when one of its places lies in a preferred country or region, and in the preferred city when both name a city; it is a mismatch when every place is known and none matches. A remote vacancy that names no place, or only worldwide ones ("Anywhere", "Remote", telecommute), is unrestricted remote and matches both location criteria. Restricted remote ("US only", "EU") is checked against the user's work authorizations, not against where the user lives: covered when an eligible authorization covers one of its countries, not covered when the user states authorizations for its countries and none is eligible, unknown when the user states none or a place is unknown. Remote restriction cannot be compared for a vacancy that is not remote. When remote work is restricted or its places are unknown and the user wants remote work, Work mode is left to Remote restriction and cannot be compared. Onsite and hybrid vacancies are checked the same way, by the vacancy's country, for the Onsite work authorization criterion; when the vacancy states that it sponsors a work visa, a missing authorization is not a mismatch. Only an authorization marked eligible covers a country. Composite labels ("Berlin, Germany", "Remote (US or Canada)") are read with a vocabulary of countries and regions; a part the vocabulary does not know is taken as a city, and a place that names no country or region is unknown.

**Time zone.** The vacancy's bands come from a stated time zone (an offset such as UTC+2 or an abbreviation such as CET, widened by "± N hours"), then from time-zone phrases in the text, then from the countries and regions of its locations.

**Buckets.** Strong is 75 and above, Partial is 55 to 74, Weak is below 55, and Not scored has no number. The Fit filter of the Inbox uses these buckets.

## Requirements

- A vacancy's requirements are the lines of its requirement sections found by code (in a text with no such section, the lines that state a requirement), each with its level, required or nice to have, from its section and marker words.
- Each requirement is decided by code against My data: ✓ when the skill is in the user's skills by the skills vocabulary or the requirement is confirmed as had; ✕ when it is in the skills the user does not have or confirmed as not had; ? otherwise. A requirement that asks for years of experience is not decided by code; the derived years are shown as evidence.
- The counter "✓ 4 · ✕ 1 · ? 6" is shown in the Inbox row and on the vacancy page.
- The requirements score is the average of ✓ = 1, ✕ = 0 and ? = 0.5, with required requirements counted twice and nice-to-have ones once. Without a decided requirement it counts as 0.5. It enters Fits you as the Requirements criterion.
- Model matching of requirements is not part of the first version. A later checker can be connected through the same interface, and only after it decides hand-labelled examples better than code.

## Facts of a vacancy

Facts are found by code, without a model, for every vacancy visible in a profile, as the background work "Analyzing vacancy" (see `application.md`): money, work mode, engagement, place, time zone, hours, required languages, the requirement lines and the offer. Structured fields the source states, the location, the tags and labelled lines ("Salary: …", "Location: …", "Time zone: …") are read first, then vocabularies over the whole text; each fact keeps the excerpt it was found in. When the address is an aggregator's, a value stated in the vacancy text replaces a field's value of the same kind; when it is the employer's own page, a field or labelled line replaces a vocabulary's value. Fits you, the requirements counter, the tiles and the offer read these facts and nothing else.

## In the lists

- Every row in all four lists shows Fits you as a filled mark with its bucket, and the requirements counter. The tooltip reads "Fits you 82: Strong", followed by the counter when requirements are read (" · ✓ 4 · ✕ 1 · ? 6").
- Empty states of Fits you: "Not compared yet", "Full job description is unavailable", "Your profile states no goals to compare".
- Empty states of the counter: "Analysing requirements…", "Not analysed yet", "No requirements stated", "Not checked yet" (no requirement is decided), "Full job description is unavailable".
- A result is out of date when the profile's goals, the work authorizations, the weights, the rates or the scoring version changed, or the vacancy's text changed. It is dimmed, still showing its old number, until the new one is ready, and the Fit filter counts it as Not scored. A change of the vacancy's title, location, tags or stated fields without a change of its text is not dimmed; its result is recomputed after the collection run that brought the change.
- **Best fit** sorts by Fits you, highest first, then by id; dimmed results and vacancies without a number come after the current ones, by id. It is the default sort of Inbox and Shortlist.
- **Newest** sorts by publication date, newest first; vacancies with no publication date come last, by when they were first seen, then by id.
- The chosen sort is remembered in the browser (see `inbox.md`).

## On the vacancy page

From top to bottom:

1. **Fits you band.** "Fits you 82 /100", the bucket and the requirements counter, and the "Refresh source" button (see `collection.md`). On the right, four tiles, each showing the vacancy's value, a line comparing it with the user's goal, and a verdict ✓, ✕ or ?:

   | Tile | Criteria behind it |
   |---|---|
   | Money | Money |
   | Location | Location, Remote restriction, Time zone, Onsite work authorization |
   | Work mode | Work mode |
   | Contract | Engagement, Hours |

   A tile shows ✕ when any of its criteria is a mismatch, ✓ when every comparable criterion matches, and ? otherwise, so a tile never disagrees with the number. Money shows the vacancy's own sum compactly: the currency symbol where one exists ($, €, £) and otherwise the code ("CHF 120k"), then the amount or the range, written in thousands or millions with one decimal when it is not whole ("$190–225k", "€7.5k", "€900–1.2M") and in full below a thousand, then the period the vacancy states ("a year"). Its conversion, "≈ €207,000", stands beside that sum and is absent when no rate is known. The Contract tile shows "Employment (assumed)" when the vacancy states no engagement. The Location tile shows its places in the list row's short form (see `inbox.md`), with the full text on hover when shortened.
2. **What they ask.** "N requirements" with the counter, then the groups Required and Nice to have. Each row shows the requirement, its alternatives, its verdict, and on the right the evidence from My data ("8 years", "Go · Rust").
   - A requirement row has two controls, one **+** ("I have this") and one **−** ("I do not have this"). On a row that names one known skill or none, each fires at once. On a row that names several known skills (including alternatives), each opens a short list with one line per named skill and a last line for the requirement as a whole: "I have <skill>" and "I have this requirement" under **+**, "I do not have <skill>" and "I do not have this requirement" under **−**.
   - **+** adds the skill to Skills at the level Proficient when the skills vocabulary knows it, otherwise it adds "You have: …" to Confirmed by you. A skill that is among the skills the user does not have is taken out of that list. "I have <skill>" adds that skill alone, so the requirement keeps its verdict and its other skills can still be answered. "I have this requirement" adds every skill the requirement names and, when that still leaves it undecided, "You have: …" to Confirmed by you.
   - **−** adds the skill to the skills the user does not have, or "You do not have: …" to Confirmed by you, and follows the same rule when the requirement names several skills. A skill that is among the user's skills is taken out of Skills.
   - After a click this vacancy is recomputed at once, on the page and in its Inbox row; other vacancies follow in the background. A notice names every change the answer made to My data, one sentence each:
     - a skill in neither list: "Added "Go" to your skills (level Proficient; change in your profile)." or "Added "SQL" to skills you do not have (change in your profile).";
     - a skill taken out of the opposite list: "Moved "Go" from skills you do not have to your skills (level Proficient; change in your profile)." or "Moved "SQL" from your skills to skills you do not have (change in your profile).", given for every skill the answer moves;
     - a statement for Confirmed by you: "Added "REST APIs SQL NoSQL" to Confirmed by you as something you have (change in your profile)." or "… as something you do not have (change in your profile).";
     - an answer that changes nothing: "Nothing new to add; your data is already up to date."
3. **What they offer** and **Listing text**, described in `inbox.md`.
4. **Shortlist check**, in Shortlist and Archive when a check exists (see below).

## Shortlist check

- When a vacancy enters Shortlist, one model call compares the user's data with the vacancy's requirements. When that call decides no requirement at all, it is made once more, and the second answer is the answer. The model receives the vacancy's facts and text, the accepted data of My data and the profile including other requirements, and the text of documents marked "Use as evidence"; never contacts, legal name or application form answers (see `profiles.md`).
- Its answer is shown next to the requirements that code left as ?, each with a short reason and the excerpt it relies on. It changes neither Fits you nor the counter.
- When any of its inputs changes, the check is marked "Based on your data of <date>" and runs again the next time the vacancy page is opened. A change of profile or My data therefore makes no model call by itself.
- States: "Checking…", "Waiting for the model: N ahead", and "Check failed: <reason>" with a "Retry" button shown only after a failure or an interruption. There is no Assess or Reassess button.
- The check uses the model chosen for it in Settings; by default it must be a local model or one the user chose explicitly.

## When things are recomputed

| Change | What is recomputed | Model calls |
|---|---|---|
| New or changed vacancy text | its facts and requirements are found; its Fits you for every profile, after that | none |
| A vacancy becomes visible in a profile without having been analysed | its facts and requirements are found | none |
| A change of goals or My data that enters Fits you | Fits you of every vacancy for the affected profiles, in the background; Shortlist checks marked outdated | none |
| + or − on a requirement | this vacancy at once, the rest in the background | none |
| A change of weights or vocabularies | everything | none |
| A new version of the application that reads texts differently | the facts of every vacancy are found again, in the background | none |
| A vacancy enters Shortlist | the Shortlist check | 1, or 2 when the first decides nothing |
| A shortlisted vacancy with an outdated check is opened | the Shortlist check | 1, or 2 when the first decides nothing |

Fits you is recomputed in the background by code, with no model call: at start, after a collection run, and after My data or a profile is saved or a profile is created. Only results that are missing or out of date are recomputed, and the work repeats until none is left, so a change made while it runs is applied too.

Vacancy texts are analysed in the background, one at a time, in the order the vacancies arrived, and only for vacancies visible in at least one profile; Fits you is recomputed for them after their analysis, not before. Analysis is code and makes no model call.

## Not carried over

- A separate You fit number; the requirements counter and the Requirements criterion replace it.
- The detailed model assessment: its summary, evidence, unknown employer details, "Confirmed mismatches", "Candidate facts / questions", provenance, provider choice, queue status line, the warnings about an older snapshot and about scores being updated, the Assess and Assess now buttons, and the queue switch in Settings. The Shortlist check replaces it.
- The model matching pass over all vacancies.
- The lists Matches, Mismatches, Missing and To clarify, which were computed and stored but no longer shown.
- The separate "Candidate facts and corrections" form with its scope; Confirmed by you holds these facts.
- A world city table for time zones; countries and regions are enough.
- Fits you shown twice on the page, and the note pointing to a tab where the assessment no longer is.
- Freshness flags of the profile and the "Refresh scoring" button.
- The background order following the sort switch.
- The "AI assessment" filter of the Inbox.
