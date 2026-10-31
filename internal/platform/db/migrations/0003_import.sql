-- Import de relevés et rapprochement du solde (jalon 3).

-- Historique des imports validés ; undone_at marque un import annulé.
CREATE TABLE imports (
    id              INTEGER PRIMARY KEY,
    account_id      INTEGER NOT NULL REFERENCES accounts (id),
    filename        TEXT    NOT NULL,
    profile         TEXT    NOT NULL,
    imported_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    lines_read      INTEGER NOT NULL,
    lines_added     INTEGER NOT NULL,
    lines_duplicate INTEGER NOT NULL,
    lines_error     INTEGER NOT NULL,
    lines_skipped   INTEGER NOT NULL,
    undone_at       TEXT
) STRICT;

-- Une opération importée garde son import (pour l'annulation) et son
-- empreinte, unique par compte : un réimport ne peut pas créer de doublon.
ALTER TABLE transactions ADD COLUMN import_id INTEGER REFERENCES imports (id);
ALTER TABLE transactions ADD COLUMN import_hash TEXT;
CREATE UNIQUE INDEX transactions_import_hash ON transactions (account_id, import_hash) WHERE import_hash IS NOT NULL;
CREATE INDEX transactions_import ON transactions (import_id);

-- Imports en cours : le fichier déposé attend dans DATA_DIR/imports/<token>,
-- ses lignes interprétées attendent dans import_lines (lues par pages, jamais
-- chargées en mémoire d'un bloc). mapping est vide tant que les colonnes ne
-- sont pas connues. created_at est en secondes Unix.
CREATE TABLE import_drafts (
    token      TEXT    PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    filename   TEXT    NOT NULL,
    format     TEXT    NOT NULL CHECK (format IN ('csv', 'xlsx', 'xls')),
    profile    TEXT    NOT NULL DEFAULT '',
    mapping    TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE import_lines (
    token    TEXT    NOT NULL REFERENCES import_drafts (token) ON DELETE CASCADE,
    line     INTEGER NOT NULL,
    date     TEXT    NOT NULL,
    label    TEXT    NOT NULL,
    amount   INTEGER NOT NULL,
    hash     TEXT    NOT NULL,
    status   TEXT    NOT NULL CHECK (status IN ('nouvelle', 'doublon', 'erreur')),
    error    TEXT    NOT NULL DEFAULT '',
    selected INTEGER NOT NULL CHECK (selected IN (0, 1)),
    PRIMARY KEY (token, line)
) STRICT, WITHOUT ROWID;

-- Dernier solde relevé sur le site de la banque, pour le rapprochement.
ALTER TABLE accounts ADD COLUMN bank_balance INTEGER;
ALTER TABLE accounts ADD COLUMN bank_balance_date TEXT;
