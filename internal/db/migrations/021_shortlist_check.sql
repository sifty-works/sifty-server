CREATE TABLE shortlist_check (
    profile    INTEGER NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
    vacancy    INTEGER NOT NULL REFERENCES vacancy(id) ON DELETE CASCADE,
    digest     TEXT    NOT NULL,
    result     TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (profile, vacancy)
) WITHOUT ROWID;
