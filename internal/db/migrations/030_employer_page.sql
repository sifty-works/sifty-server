CREATE TABLE employer_page (
    vacancy     INTEGER PRIMARY KEY REFERENCES vacancy(id) ON DELETE CASCADE,
    url         TEXT    NOT NULL,
    system      TEXT    NOT NULL,
    way         TEXT    NOT NULL,
    searched_at TEXT    NOT NULL
) WITHOUT ROWID;
