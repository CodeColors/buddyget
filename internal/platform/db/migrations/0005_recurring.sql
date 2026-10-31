-- Opérations récurrentes (jalon 5).

-- Une récurrence : tous les period_count jours, semaines ou mois à partir de
-- start_date (dernier jour du mois si last_day), jusqu'à end_date
-- facultative. amount est signé ; estimated marque un montant variable.
-- signature identifie la série (compte, sens, libellé normalisé) pour ne pas
-- la proposer de nouveau à la détection.
CREATE TABLE recurring_rules (
    id           INTEGER PRIMARY KEY,
    label        TEXT    NOT NULL,
    account_id   INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    amount       INTEGER NOT NULL,
    estimated    INTEGER NOT NULL DEFAULT 0 CHECK (estimated IN (0, 1)),
    period_count INTEGER NOT NULL CHECK (period_count >= 1),
    period_unit  TEXT    NOT NULL CHECK (period_unit IN ('jour', 'semaine', 'mois')),
    last_day     INTEGER NOT NULL DEFAULT 0 CHECK (last_day IN (0, 1)),
    start_date   TEXT    NOT NULL,
    end_date     TEXT,
    category_id  INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    origin       TEXT    NOT NULL CHECK (origin IN ('manuelle', 'detectee')),
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    signature    TEXT    NOT NULL
) STRICT;

CREATE INDEX recurring_rules_account ON recurring_rules (account_id);
CREATE INDEX recurring_rules_signature ON recurring_rules (signature);

-- Séries rejetées à la détection : elles ne sont plus proposées.
CREATE TABLE recurring_dismissals (
    signature TEXT PRIMARY KEY
) STRICT, WITHOUT ROWID;
