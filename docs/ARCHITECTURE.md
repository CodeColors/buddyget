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
├── platform/db/       ouverture SQLite (WAL, pools écriture/lecture), migrateur
│   └── migrations/    NNNN_description.sql, embarqués
├── auth/              comptes, argon2id, limitation des tentatives, sessions, thème
└── web/               routage, middlewares, handlers, rendu
    ├── templates/     layout.html + pages/<page>.html
    └── static/        CSS, icône, manifeste PWA (embarqués)
e2e/                   tests Playwright (développement et CI uniquement)
```

Les packages métier prévus par le plan (`ledger`, `importer`, `forecast`,
`savings`, `budget`, `alerts`…) s'ajoutent jalon par jalon sur ce modèle.

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

## Interface

- Rendu serveur `html/template` ; une page = `templates/pages/<nom>.html`
  définissant le bloc `content`, insérée dans `layout.html`.
- Mobile d'abord : barre d'onglets en bas sous 768 px, sous l'en-tête au-delà ;
  cibles tactiles de 44 px, champs de 16 px.
- Thème rendu côté serveur (`data-theme`), couleurs en `light-dark()`.
- Fichiers statiques servis depuis la mémoire, URL versionnées par empreinte.
