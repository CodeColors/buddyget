-- Partage du foyer et sauvegardes (jalon 11).

-- Membre du foyer, distinct d'un compte utilisateur. income : revenu
-- mensuel, pour répartir une dépense au prorata des revenus.
CREATE TABLE household_members (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    income   INTEGER NOT NULL DEFAULT 0 CHECK (income >= 0),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1))
) STRICT;

-- Opération partagée : qui l'a payée et comment elle se répartit.
CREATE TABLE shared_transactions (
    transaction_id INTEGER PRIMARY KEY REFERENCES transactions (id) ON DELETE CASCADE,
    payer_id       INTEGER NOT NULL REFERENCES household_members (id),
    mode           TEXT    NOT NULL CHECK (mode IN ('egal', 'prorata', 'libre'))
) STRICT;

-- Part de chaque membre, en centimes ; la somme vaut la valeur absolue du
-- montant de l'opération.
CREATE TABLE transaction_splits (
    transaction_id INTEGER NOT NULL REFERENCES shared_transactions (transaction_id) ON DELETE CASCADE,
    member_id      INTEGER NOT NULL REFERENCES household_members (id),
    share          INTEGER NOT NULL CHECK (share >= 0),
    PRIMARY KEY (transaction_id, member_id)
) STRICT;

CREATE INDEX transaction_splits_member ON transaction_splits (member_id);
CREATE INDEX shared_transactions_payer ON shared_transactions (payer_id);

-- Remboursement entre membres ; transfer_id : le virement interne qui le
-- matérialise, quand les deux comptes sont suivis.
CREATE TABLE member_settlements (
    id          INTEGER PRIMARY KEY,
    date        TEXT    NOT NULL,
    from_id     INTEGER NOT NULL REFERENCES household_members (id),
    to_id       INTEGER NOT NULL REFERENCES household_members (id),
    amount      INTEGER NOT NULL CHECK (amount > 0),
    transfer_id INTEGER REFERENCES transfers (id) ON DELETE SET NULL,
    CHECK (from_id <> to_id)
) STRICT;

-- Réglages des sauvegardes (une ligne) : sauvegarde automatique, nombre
-- d'archives conservées, copie vers un second emplacement (dossier monté ou
-- WebDAV) et compte rendu de la dernière sauvegarde.
CREATE TABLE backup_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    auto            INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1)),
    keep            INTEGER NOT NULL DEFAULT 7 CHECK (keep BETWEEN 1 AND 90),
    copy_kind       TEXT    NOT NULL DEFAULT 'aucune' CHECK (copy_kind IN ('aucune', 'dossier', 'webdav')),
    copy_dir        TEXT    NOT NULL DEFAULT '',
    webdav_url      TEXT    NOT NULL DEFAULT '',
    webdav_user     TEXT    NOT NULL DEFAULT '',
    webdav_password TEXT    NOT NULL DEFAULT '',
    last_at         TEXT    NOT NULL DEFAULT '',
    last_error      TEXT    NOT NULL DEFAULT ''
) STRICT;

INSERT INTO backup_settings (id) VALUES (1);
