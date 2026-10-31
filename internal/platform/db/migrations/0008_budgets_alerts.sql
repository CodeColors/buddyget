-- Budgets par enveloppe et alertes (jalon 8).

-- Plafond mensuel d'une catégorie de dépenses (sous-catégories comprises,
-- sauf celles qui ont leur propre budget). Avec carry_over, le reste ou le
-- dépassement d'un mois s'ajoute au mois suivant, à partir du mois since
-- (AAAA-MM).
CREATE TABLE budgets (
    category_id INTEGER PRIMARY KEY REFERENCES categories (id) ON DELETE CASCADE,
    amount      INTEGER NOT NULL CHECK (amount > 0),
    carry_over  INTEGER NOT NULL DEFAULT 0 CHECK (carry_over IN (0, 1)),
    since       TEXT    NOT NULL
) STRICT, WITHOUT ROWID;

-- Alertes : key identifie la situation signalée (une seule alerte par
-- situation, même acquittée). created_on est une date AAAA-MM-JJ ; notified
-- marque une alerte déjà envoyée (ou écartée) par les canaux sortants.
CREATE TABLE alerts (
    id           INTEGER PRIMARY KEY,
    kind         TEXT    NOT NULL,
    key          TEXT    NOT NULL UNIQUE,
    title        TEXT    NOT NULL,
    detail       TEXT    NOT NULL DEFAULT '',
    link         TEXT    NOT NULL DEFAULT '',
    created_on   TEXT    NOT NULL,
    acknowledged INTEGER NOT NULL DEFAULT 0 CHECK (acknowledged IN (0, 1)),
    notified     INTEGER NOT NULL DEFAULT 0 CHECK (notified IN (0, 1))
) STRICT;

CREATE INDEX alerts_open ON alerts (acknowledged, created_on);
CREATE INDEX alerts_pending ON alerts (notified) WHERE notified = 0;

-- Réglage de chaque type d'alerte : activé, et son paramètre (seuil en %,
-- facteur ou nombre de jours selon le type).
CREATE TABLE alert_settings (
    kind    TEXT    PRIMARY KEY,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    param   INTEGER NOT NULL CHECK (param > 0)
) STRICT, WITHOUT ROWID;

INSERT INTO alert_settings (kind, enabled, param) VALUES
    ('budget', 1, 80),
    ('prevision', 1, 30),
    ('inhabituelle', 1, 3),
    ('abonnement', 1, 10),
    ('echeance', 1, 3),
    ('import', 1, 10),
    ('deblocage', 1, 30);

-- Canaux sortants, désactivés par défaut (une seule ligne).
CREATE TABLE notification_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    email_enabled   INTEGER NOT NULL DEFAULT 0 CHECK (email_enabled IN (0, 1)),
    smtp_host       TEXT    NOT NULL DEFAULT '',
    smtp_port       INTEGER NOT NULL DEFAULT 587,
    smtp_security   TEXT    NOT NULL DEFAULT 'starttls' CHECK (smtp_security IN ('starttls', 'tls')),
    smtp_user       TEXT    NOT NULL DEFAULT '',
    smtp_password   TEXT    NOT NULL DEFAULT '',
    email_from      TEXT    NOT NULL DEFAULT '',
    email_to        TEXT    NOT NULL DEFAULT '',
    webhook_enabled INTEGER NOT NULL DEFAULT 0 CHECK (webhook_enabled IN (0, 1)),
    webhook_url     TEXT    NOT NULL DEFAULT '',
    frequency       TEXT    NOT NULL DEFAULT 'immediate' CHECK (frequency IN ('immediate', 'daily', 'weekly')),
    discreet        INTEGER NOT NULL DEFAULT 1 CHECK (discreet IN (0, 1))
) STRICT;

INSERT INTO notification_settings (id) VALUES (1);
