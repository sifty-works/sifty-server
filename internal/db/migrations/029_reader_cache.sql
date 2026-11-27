CREATE TABLE reader_cache (
    reader     TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    version    INTEGER NOT NULL,
    hash       TEXT    NOT NULL,
    answer     TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (reader, subject)
) WITHOUT ROWID;
