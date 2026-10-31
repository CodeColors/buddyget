-- Prévisionnel (jalon 6).

-- Report d'une échéance qui tombe un jour non ouvré.
ALTER TABLE recurring_rules ADD COLUMN business_day TEXT NOT NULL DEFAULT 'suivant'
    CHECK (business_day IN ('suivant', 'precedent', 'aucun'));

-- Échéances écartées ponctuellement du prévisionnel, repérées par leur date
-- théorique (avant report) : elles le restent si la règle de report change.
CREATE TABLE forecast_exclusions (
    rule_id INTEGER NOT NULL REFERENCES recurring_rules (id) ON DELETE CASCADE,
    date    TEXT    NOT NULL,
    PRIMARY KEY (rule_id, date)
) STRICT, WITHOUT ROWID;

-- Réglages du prévisionnel, communs au foyer (une seule ligne). threshold
-- est le seuil d'alerte en centimes ; average_months la profondeur de la
-- moyenne glissante des dépenses variables (0 : non estimées).
CREATE TABLE forecast_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    threshold      INTEGER NOT NULL DEFAULT 0,
    cleared_only   INTEGER NOT NULL DEFAULT 0 CHECK (cleared_only IN (0, 1)),
    average_months INTEGER NOT NULL DEFAULT 3 CHECK (average_months IN (0, 3, 6, 12))
) STRICT;

INSERT INTO forecast_settings (id) VALUES (1);
