# Architecture

BuddyGet est un binaire Go unique : serveur HTTP, rendu des pages, fichiers
statiques et migrations y sont embarqués. Les données tiennent dans un seul
fichier SQLite du dossier `DATA_DIR`.

## Principes

- **Un seul mécanisme par besoin** : chaque brique est un package unique,
  appelé par les autres ; aucune logique métier dans les handlers HTTP.
- **Domaine pur, testable sans base** : les calculs sont des fonctions pures
  sur des types simples ; la persistance reste dans le package concerné.
- **Bibliothèque standard d'abord** : une dépendance n'entre que si elle
  remplace beaucoup de code risqué (pilote SQLite, argon2id).
- **Montants en centimes `int64`**, dates civiles `AAAA-MM-JJ`.
- **Aucune tâche de fond permanente** : les travaux d'entretien se font à
  l'occasion d'une action (ex. purge des sessions à la connexion).

## Packages

```
cmd/buddyget/          point d'entrée : configuration, base, serveur, arrêt propre,
                       sous-commande healthcheck
internal/
├── config/            variables d'environnement → Config validée
├── platform/
│   ├── db/            ouverture SQLite (WAL, pools écriture/lecture), transactions,
│   │   └── migrations/ migrateur ; NNNN_description.sql embarqués
│   ├── money/         montants en centimes : saisie et affichage à la française
│   ├── calendar/      dates civiles AAAA-MM-JJ
│   ├── text/          forme de comparaison des textes (casse, accents, espaces)
│   └── validation/    erreur de saisie commune (message destiné à l'utilisateur)
├── auth/              comptes, argon2id, limitation des tentatives, sessions, thème
├── ledger/            grand livre : comptes, catégories, opérations, virements,
│                      recherche (Filter), édition par lot, rapprochement,
│                      détection des virements internes
├── categorize/        règles de catégorisation : moteur, application rétroactive
├── importer/          import : lecteurs CSV/XLSX/XLS, profils, Mapping, déduplication,
│   │                  brouillon, validation, historique, annulation
│   └── profiles/      sg.yaml, bnp.yaml, generique.yaml (embarqués)
└── web/               routage, middlewares, handlers par écran, rendu
    ├── templates/     layout.html + pages/<page>.html
    └── static/        CSS, app.js, HTMX (vendorisé), icône, manifeste PWA
e2e/                   tests Playwright (développement et CI uniquement)
testdata/              relevés d'exemple (synthétiques, voir testdata/README.md)
```

Les packages métier suivants (`recurring`,
`forecast`, `savings`, `budget`, `alerts`…) s'ajoutent jalon par jalon sur ce
modèle : un service par domaine, appelé par les handlers.

## Chemin d'une requête

```
securityHeaders → logRequests → CrossOriginProtection → ServeMux
                                                         ├─ /healthz, /static/{file}
                                                         ├─ /setup, /login, /logout
                                                         └─ requireUser → pages
```

1. `securityHeaders` pose la CSP stricte et les en-têtes de sécurité.
2. `logRequests` journalise méthode, chemin et statut (niveau `debug`).
3. `CrossOriginProtection` (bibliothèque standard) rejette toute requête non
   sûre venant d'une autre origine : c'est la protection CSRF.
4. `requireUser` lit le cookie de session, prolonge la session au besoin et
   place l'utilisateur dans le contexte ; sinon, redirection vers `/login`.
5. Le handler délègue au service métier, puis `render` exécute la page dans un
   tampon (une erreur de gabarit ne produit jamais de page tronquée).

Tant qu'aucun compte n'existe, `/login` redirige vers `/setup` (création du
premier compte). La création est atomique : deux requêtes simultanées ne
peuvent pas créer deux premiers comptes.

## Base de données

- Écritures : une connexion unique (`DB.W`), transactions `BEGIN IMMEDIATE`.
- Lectures : pool `DB.R` de 4 connexions en `query_only`.
- Schéma : `internal/platform/db/migrations/`, appliqué au démarrage.

| Table | Rôle |
|---|---|
| `schema_migrations` | versions de schéma appliquées |
| `users` | comptes (identifiant insensible à la casse, empreinte argon2id, thème) |
| `sessions` | empreinte SHA-256 du jeton, utilisateur, échéance (secondes Unix) |
| `accounts` | comptes du foyer : type, banque, IBAN masqué, solde initial et sa date, archivage |
| `categories` | deux niveaux (`parent_id`), type dépense / revenu / virement, couleur |
| `transactions` | opérations : compte, date, libellé, montant signé en centimes, catégorie, note, pointage, virement |
| `transfers` | lien entre les deux opérations d'un virement interne |
| `transactions_fts` | index plein texte (FTS5) du libellé et de la note, tenu à jour par déclencheurs |
| `imports` | historique des imports validés : fichier, profil, décomptes, annulation |
| `import_drafts`, `import_lines` | imports en cours : fichier déposé, correspondance, lignes interprétées, catégorie suggérée et sélection |
| `category_rules` | règles : motif, « contient » ou expression régulière, catégorie, priorité |
| `transfer_dismissals` | paires écartées de la détection des virements internes |

Une opération importée porte `import_id` (annulation) et `import_hash`
(empreinte unique par compte, garde-fou contre les doublons). Le compte porte
le dernier solde bancaire relevé (`bank_balance`, `bank_balance_date`).

L'index `(account_id, date, amount)` couvre le calcul des soldes sans lire la
table des opérations.

## Écrans et routes

| Route | Écran |
|---|---|
| `/` | tableau de bord : soldes par compte et total |
| `/operations` | liste paginée, recherche, filtres, tri, édition par lot (`POST /operations/lot`) |
| `/operations/nouvelle`, `/operations/{id}` | saisie et modification (opération ou virement) |
| `/comptes`, `/comptes/nouveau`, `/comptes/{id}` | comptes : liste, création, modification, archivage |
| `/categories`, `/categories/nouvelle`, `/categories/{id}` | catégories et sous-catégories |
| `/import` | étape 1 : dépôt du fichier et choix du compte, imports en cours |
| `/import/{jeton}`, `/import/{jeton}/colonnes` | étape 2 : choix des colonnes si besoin, aperçu et sélection, validation |
| `/comptes/{id}/rapprochement` | étape 3 : solde de la banque, écart, opérations non pointées |
| `/import/historique` | imports validés, annulation |
| `/regles`, `/regles/nouvelle`, `/regles/{id}` | règles de catégorisation, application aux opérations sans catégorie |
| `/operations/virements` | virements internes possibles : relier ou écarter |

### Chemin d'un import

```
dépôt ─► fichier copié dans DATA_DIR/imports/<jeton> ─► format (signature)
      ─► 30 premières lignes ─► profil reconnu ? ─┬─ oui ─► Mapping
                                                  └─ non ─► choix des colonnes ─► Mapping
Mapping ─► lecture en flux ─► lignes interprétées + empreintes ─► import_lines (par lots)
        (catégorie suggérée par les règles)
aperçu (pages de 100) ─► sélection ─► INSERT … SELECT dans transactions ─► imports
                                              └─► virements internes possibles signalés
```

Toute action renvoie (redirection 303) vers la page d'origine avec un message
de confirmation. Les requêtes HTMX de la recherche ne reçoivent que le bloc
des résultats (`renderBlock`).

## Interface

- Rendu serveur `html/template` ; une page = `templates/pages/<nom>.html`
  définissant le bloc `content`, insérée dans `layout.html`.
- Mobile d'abord : barre d'onglets en bas sous 768 px, sous l'en-tête au-delà ;
  cibles tactiles de 44 px, champs de 16 px.
- Thème rendu côté serveur (`data-theme`), couleurs en `light-dark()`.
- Fichiers statiques servis depuis la mémoire (compressés une fois au
  démarrage), URL versionnées par empreinte.
- Opérations en cartes sur mobile, en colonnes alignées au-delà de 768 px ;
  bouton flottant « + Opération » sur toutes les pages.
- Amélioration progressive : chaque écran fonctionne sans JavaScript ; HTMX
  rend la recherche instantanée, `app.js` ajoute confirmations et « tout cocher ».
