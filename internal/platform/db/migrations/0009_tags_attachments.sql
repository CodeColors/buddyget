-- Tags, pièces jointes, saisie rapide et abonnements (jalon 9).

-- Étiquettes libres, en complément des catégories.
CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    name TEXT    NOT NULL UNIQUE COLLATE NOCASE
) STRICT;

CREATE TABLE transaction_tags (
    transaction_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    tag_id         INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (transaction_id, tag_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX transaction_tags_tag ON transaction_tags (tag_id);

-- Pièce jointe d'une opération : le fichier DATA_DIR/attachments/<file>
-- (image réduite en JPEG, ou PDF tel quel) ; size en octets.
CREATE TABLE attachments (
    id             INTEGER PRIMARY KEY,
    transaction_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('image', 'pdf')),
    size           INTEGER NOT NULL,
    file           TEXT    NOT NULL UNIQUE,
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
) STRICT;

CREATE INDEX attachments_transaction ON attachments (transaction_id);

-- Modèle de saisie fréquente : un clic crée l'opération (amount signé).
CREATE TABLE quick_templates (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    account_id  INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    label       TEXT    NOT NULL,
    amount      INTEGER NOT NULL,
    category_id INTEGER REFERENCES categories (id) ON DELETE SET NULL
) STRICT;

-- Récurrence marquée « abonnement » (vue Abonnements).
ALTER TABLE recurring_rules ADD COLUMN subscription INTEGER NOT NULL DEFAULT 0 CHECK (subscription IN (0, 1));
