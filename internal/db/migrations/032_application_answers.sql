ALTER TABLE application_field ADD COLUMN answer   TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE application_field ADD COLUMN source   TEXT    NOT NULL DEFAULT '';
ALTER TABLE application_field ADD COLUMN note     TEXT    NOT NULL DEFAULT '';
ALTER TABLE application_field ADD COLUMN evidence TEXT    NOT NULL DEFAULT '';
ALTER TABLE application_field ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
