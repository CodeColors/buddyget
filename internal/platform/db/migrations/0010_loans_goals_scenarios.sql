-- Crédits, projets, lissage et scénarios (jalon 10).

-- Crédit amortissable à mensualités constantes. Taux nominal annuel en
-- millièmes de point (3 450 = 3,450 %), assurance mensuelle en centimes.
-- La mensualité est une récurrence générée (rule_id) : pas de double saisie.
CREATE TABLE loans (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    account_id    INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    principal     INTEGER NOT NULL CHECK (principal > 0),
    rate          INTEGER NOT NULL CHECK (rate BETWEEN 0 AND 100000),
    months        INTEGER NOT NULL CHECK (months BETWEEN 1 AND 600),
    insurance     INTEGER NOT NULL DEFAULT 0 CHECK (insurance >= 0),
    first_payment TEXT    NOT NULL,
    rule_id       INTEGER REFERENCES recurring_rules (id) ON DELETE SET NULL
) STRICT;

-- Remboursements anticipés effectués : la mensualité reste, la durée raccourcit.
CREATE TABLE loan_prepayments (
    id      INTEGER PRIMARY KEY,
    loan_id INTEGER NOT NULL REFERENCES loans (id) ON DELETE CASCADE,
    date    TEXT    NOT NULL,
    amount  INTEGER NOT NULL CHECK (amount > 0)
) STRICT;

CREATE INDEX loan_prepayments_loan ON loan_prepayments (loan_id);

-- Projet : montant cible à une date, rattaché à un compte d'épargne dont il
-- suit une part (cagnotte virtuelle, saved). tag rattache ses dépenses ;
-- rule_id est le versement programmé créé pour l'alimenter.
CREATE TABLE goals (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    target      INTEGER NOT NULL CHECK (target > 0),
    target_date TEXT    NOT NULL,
    account_id  INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    saved       INTEGER NOT NULL DEFAULT 0 CHECK (saved >= 0),
    tag         TEXT    NOT NULL DEFAULT '',
    rule_id     INTEGER REFERENCES recurring_rules (id) ON DELETE SET NULL
) STRICT;

-- Lissage d'une récurrence espacée (annuelle…) : provision mensuelle, mise
-- de côté sur un compte d'épargne (smooth_account_id) ou sans compte.
ALTER TABLE recurring_rules ADD COLUMN smooth INTEGER NOT NULL DEFAULT 0 CHECK (smooth IN (0, 1));
ALTER TABLE recurring_rules ADD COLUMN smooth_account_id INTEGER REFERENCES accounts (id) ON DELETE SET NULL;

-- Scénario « et si » : calque nommé de modifications du prévisionnel.
CREATE TABLE scenarios (
    id   INTEGER PRIMARY KEY,
    name TEXT    NOT NULL UNIQUE COLLATE NOCASE
) STRICT;

-- Modification d'un scénario. Selon kind : ponctuel (account, date, amount
-- signé), recurrent (account, date de début, end_date, months entre deux
-- échéances, amount signé), suspension (rule, date d'effet), variation
-- (rule, date d'effet, amount ou percent en centièmes de point), credit
-- (account, date de 1re échéance, amount emprunté, rate, months,
-- insurance), anticipe (loan, date, amount remboursé).
CREATE TABLE scenario_items (
    id          INTEGER PRIMARY KEY,
    scenario_id INTEGER NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL CHECK (kind IN ('ponctuel', 'recurrent', 'suspension', 'variation', 'credit', 'anticipe')),
    label       TEXT    NOT NULL DEFAULT '',
    account_id  INTEGER REFERENCES accounts (id) ON DELETE CASCADE,
    rule_id     INTEGER REFERENCES recurring_rules (id) ON DELETE CASCADE,
    loan_id     INTEGER REFERENCES loans (id) ON DELETE CASCADE,
    amount      INTEGER NOT NULL DEFAULT 0,
    percent     INTEGER NOT NULL DEFAULT 0,
    rate        INTEGER NOT NULL DEFAULT 0,
    months      INTEGER NOT NULL DEFAULT 0,
    insurance   INTEGER NOT NULL DEFAULT 0,
    date        TEXT    NOT NULL,
    end_date    TEXT
) STRICT;

CREATE INDEX scenario_items_scenario ON scenario_items (scenario_id);
