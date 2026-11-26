CREATE TABLE fact (
    id             INTEGER PRIMARY KEY,
    vacancy        INTEGER NOT NULL REFERENCES vacancy(id) ON DELETE CASCADE,
    version        INTEGER NOT NULL,
    worker         TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    value          TEXT    NOT NULL,
    flag           TEXT    NOT NULL DEFAULT '',
    quote_start    INTEGER NOT NULL DEFAULT 0,
    quote_end      INTEGER NOT NULL DEFAULT 0,
    way            TEXT    NOT NULL,
    origin         TEXT    NOT NULL,
    worker_version INTEGER NOT NULL
);

CREATE INDEX fact_vacancy ON fact (vacancy, version);
