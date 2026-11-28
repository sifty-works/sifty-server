CREATE TABLE application_form (
    vacancy INTEGER PRIMARY KEY REFERENCES vacancy(id) ON DELETE CASCADE,
    url     TEXT    NOT NULL,
    system  TEXT    NOT NULL DEFAULT '',
    state   TEXT    NOT NULL,
    reason  TEXT    NOT NULL DEFAULT '',
    read_at TEXT    NOT NULL
);

CREATE TABLE application_field (
    form     INTEGER NOT NULL REFERENCES application_form(vacancy) ON DELETE CASCADE,
    key      TEXT    NOT NULL,
    position INTEGER NOT NULL,
    label    TEXT    NOT NULL,
    kind     TEXT    NOT NULL,
    required INTEGER NOT NULL,
    options  TEXT    NOT NULL DEFAULT '[]',
    PRIMARY KEY (form, key)
) WITHOUT ROWID;
