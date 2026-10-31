-- Épargne (jalon 7).

-- Réglages d'un compte d'épargne. Taux et pénalité en centièmes de point
-- (170 = 1,70 %), plafond en centimes ; 0 : sans. Les fonds sont bloqués
-- jusqu'à unlock_date, ou chaque versement l'est lock_months mois (jamais
-- les deux). since est la date du premier enregistrement : les intérêts ne
-- sont inscrits automatiquement qu'à partir de cette année-là.
CREATE TABLE savings_settings (
    account_id    INTEGER PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    product       TEXT    NOT NULL
        CHECK (product IN ('livret_a', 'ldds', 'lep', 'pel', 'livret', 'assurance_vie', 'cat', 'autre')),
    rate          INTEGER NOT NULL DEFAULT 0 CHECK (rate BETWEEN 0 AND 10000),
    method        TEXT    NOT NULL CHECK (method IN ('aucun', 'simple', 'compose_annuel', 'quinzaine')),
    auto_interest INTEGER NOT NULL DEFAULT 1 CHECK (auto_interest IN (0, 1)),
    ceiling       INTEGER NOT NULL DEFAULT 0 CHECK (ceiling >= 0),
    unlock_date   TEXT,
    lock_months   INTEGER NOT NULL DEFAULT 0 CHECK (lock_months BETWEEN 0 AND 600),
    penalty       INTEGER NOT NULL DEFAULT 0 CHECK (penalty BETWEEN 0 AND 10000),
    since         TEXT    NOT NULL,
    CHECK (unlock_date IS NULL OR lock_months = 0)
) STRICT, WITHOUT ROWID;

-- Écriture d'intérêts : l'année qu'elle rémunère, une seule par compte.
ALTER TABLE transactions ADD COLUMN interest_year INTEGER;
CREATE UNIQUE INDEX transactions_interest ON transactions (account_id, interest_year) WHERE interest_year IS NOT NULL;

-- Versement programmé : une récurrence peut être un virement vers un autre
-- compte du foyer (amount reste le montant débité, négatif).
ALTER TABLE recurring_rules ADD COLUMN to_account_id INTEGER REFERENCES accounts (id) ON DELETE CASCADE;
