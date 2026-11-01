-- Comptes, catégories, opérations et virements (jalon 2).

-- Le solde initial est le solde au début de opening_date : les opérations
-- datées de ce jour ou après s'y ajoutent. Seul un IBAN masqué est conservé.
CREATE TABLE accounts (
    id              INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    kind            TEXT    NOT NULL CHECK (kind IN ('courant', 'epargne', 'especes', 'autre')),
    bank            TEXT    NOT NULL CHECK (bank IN ('SG', 'BNP', 'autre')),
    iban_masked     TEXT    NOT NULL DEFAULT '',
    opening_balance INTEGER NOT NULL DEFAULT 0,
    opening_date    TEXT    NOT NULL,
    archived        INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1))
) STRICT;

-- Deux niveaux au plus (contrôlé par l'application) ; une sous-catégorie a
-- le type de son parent.
CREATE TABLE categories (
    id        INTEGER PRIMARY KEY,
    parent_id INTEGER REFERENCES categories (id) ON DELETE CASCADE,
    name      TEXT    NOT NULL,
    kind      TEXT    NOT NULL CHECK (kind IN ('depense', 'revenu', 'virement')),
    color     TEXT    NOT NULL
) STRICT;

CREATE UNIQUE INDEX categories_name ON categories (coalesce(parent_id, 0), name COLLATE NOCASE);

-- Un virement entre comptes du foyer relie deux opérations de signes opposés.
CREATE TABLE transfers (
    id INTEGER PRIMARY KEY
) STRICT;

-- Montants signés en centimes, dates AAAA-MM-JJ.
CREATE TABLE transactions (
    id          INTEGER PRIMARY KEY,
    account_id  INTEGER NOT NULL REFERENCES accounts (id),
    date        TEXT    NOT NULL,
    label       TEXT    NOT NULL,
    amount      INTEGER NOT NULL,
    category_id INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    note        TEXT    NOT NULL DEFAULT '',
    cleared     INTEGER NOT NULL DEFAULT 0 CHECK (cleared IN (0, 1)),
    transfer_id INTEGER REFERENCES transfers (id) ON DELETE CASCADE
) STRICT;

-- (account_id, date, amount) couvre le calcul des soldes sans lire la table.
CREATE INDEX transactions_account_date ON transactions (account_id, date, amount);
CREATE INDEX transactions_date ON transactions (date);
CREATE INDEX transactions_category ON transactions (category_id);
CREATE INDEX transactions_transfer ON transactions (transfer_id);

-- Recherche plein texte, insensible à la casse et aux accents, synchronisée
-- par déclencheurs (index externe : le texte n'est pas dupliqué).
CREATE VIRTUAL TABLE transactions_fts USING fts5 (
    label, note,
    content = 'transactions', content_rowid = 'id',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER transactions_fts_insert AFTER INSERT ON transactions BEGIN
    INSERT INTO transactions_fts (rowid, label, note) VALUES (new.id, new.label, new.note);
END;

CREATE TRIGGER transactions_fts_delete AFTER DELETE ON transactions BEGIN
    INSERT INTO transactions_fts (transactions_fts, rowid, label, note) VALUES ('delete', old.id, old.label, old.note);
END;

CREATE TRIGGER transactions_fts_update AFTER UPDATE OF label, note ON transactions BEGIN
    INSERT INTO transactions_fts (transactions_fts, rowid, label, note) VALUES ('delete', old.id, old.label, old.note);
    INSERT INTO transactions_fts (rowid, label, note) VALUES (new.id, new.label, new.note);
END;

-- Catégories par défaut, modifiables dans l'interface.
INSERT INTO categories (name, kind, color) VALUES
    ('Logement', 'depense', '#8d6e63'),
    ('Alimentation', 'depense', '#43a047'),
    ('Transport', 'depense', '#1e88e5'),
    ('Santé', 'depense', '#e53935'),
    ('Loisirs', 'depense', '#8e24aa'),
    ('Enfants', 'depense', '#fb8c00'),
    ('Shopping', 'depense', '#d81b60'),
    ('Impôts et taxes', 'depense', '#546e7a'),
    ('Banque', 'depense', '#00897b'),
    ('Divers', 'depense', '#757575'),
    ('Salaires', 'revenu', '#2e7d32'),
    ('Allocations', 'revenu', '#00838f'),
    ('Remboursements', 'revenu', '#6d4c41'),
    ('Autres revenus', 'revenu', '#558b2f'),
    ('Épargne', 'virement', '#3949ab');

WITH c (parent, name) AS (VALUES
    ('Logement', 'Loyer ou crédit'),
    ('Logement', 'Énergie'),
    ('Logement', 'Eau'),
    ('Logement', 'Assurance habitation'),
    ('Logement', 'Internet et téléphone'),
    ('Alimentation', 'Courses'),
    ('Alimentation', 'Restaurants'),
    ('Transport', 'Carburant'),
    ('Transport', 'Transports en commun'),
    ('Transport', 'Entretien et assurance'),
    ('Santé', 'Médecin'),
    ('Santé', 'Pharmacie'),
    ('Santé', 'Mutuelle'),
    ('Loisirs', 'Sorties'),
    ('Loisirs', 'Voyages'),
    ('Loisirs', 'Abonnements'),
    ('Enfants', 'Garde'),
    ('Enfants', 'Scolarité'),
    ('Shopping', 'Vêtements'),
    ('Shopping', 'Maison'),
    ('Impôts et taxes', 'Impôt sur le revenu'),
    ('Impôts et taxes', 'Taxe foncière'),
    ('Banque', 'Frais bancaires')
)
INSERT INTO categories (parent_id, name, kind, color)
SELECT p.id, c.name, p.kind, p.color
FROM c JOIN categories p ON p.name = c.parent AND p.parent_id IS NULL;
