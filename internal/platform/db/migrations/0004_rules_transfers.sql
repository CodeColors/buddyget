-- Règles de catégorisation et détection des virements internes (jalon 4).

-- Une règle catégorise les opérations dont le libellé contient un texte
-- (casse et accents ignorés) ou vérifie une expression régulière. La plus
-- prioritaire l'emporte.
CREATE TABLE category_rules (
    id          INTEGER PRIMARY KEY,
    pattern     TEXT    NOT NULL,
    match       TEXT    NOT NULL CHECK (match IN ('contient', 'regex')),
    category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    priority    INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX category_rules_category ON category_rules (category_id);

-- Catégorie suggérée par les règles pour une ligne en attente d'import.
ALTER TABLE import_lines ADD COLUMN category_id INTEGER REFERENCES categories (id) ON DELETE SET NULL;

-- Paires écartées par l'utilisateur : elles ne sont plus proposées comme
-- virement interne.
CREATE TABLE transfer_dismissals (
    debit_id  INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    credit_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    PRIMARY KEY (debit_id, credit_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX transfer_dismissals_credit ON transfer_dismissals (credit_id);

-- Recherche des montants opposés pour la détection des virements.
CREATE INDEX transactions_amount_date ON transactions (amount, date);
