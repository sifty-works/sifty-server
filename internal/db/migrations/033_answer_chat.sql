CREATE TABLE answer_chat (
    form       INTEGER NOT NULL REFERENCES application_form(vacancy) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    role       TEXT    NOT NULL,
    text       TEXT    NOT NULL,
    field      TEXT    NOT NULL DEFAULT '',
    proposal   TEXT    NOT NULL DEFAULT '[]',
    created_at TEXT    NOT NULL,
    PRIMARY KEY (form, position)
) WITHOUT ROWID;

ALTER TABLE qa ADD COLUMN company TEXT NOT NULL DEFAULT '';
