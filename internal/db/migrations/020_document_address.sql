ALTER TABLE document ADD COLUMN address TEXT NOT NULL DEFAULT '';
ALTER TABLE document ADD COLUMN address_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX document_address_key ON document (address_key) WHERE address_key != '';
