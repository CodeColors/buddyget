# BuddyGet v1 : plan d'implémentation

Date : 31/10/2026. Réf. : [cahier-des-charges.md](cahier-des-charges.md). Les écarts au plan sont consignés dans [DECISIONS.md](DECISIONS.md).

## 0. Réponses aux questions bloquantes (31/10/2026)

| Question | Décision |
|---|---|
| Hébergement du code | Dépôt GitHub privé **`CodeColors/buddyget`**, branche `main` ; commit à chaque jalon, CI GitHub Actions |
| Driver SQLite | `modernc.org/sqlite` |
| Exports SG / BNP | Fichiers réels anonymisés fournis par l'utilisateur avant le jalon 3 |
| Licence | AGPL-3.0 |

**Avancement** : jalons 1 et 2 livrés le 31/10/2026.

## 1. Principes directeurs

- **Un seul mécanisme par besoin** : chaque brique est un package Go unique, les autres l'appellent ; aucune logique métier dans les handlers HTTP.
- **Domaine pur, testable sans base** : les calculs (dédup, récurrences, prévisionnel, intérêts, amortissement, jours ouvrés) sont des fonctions pures sur des types simples ; la persistance est un « store » séparé par package.
- **Sobriété** : bibliothèque standard d'abord (`net/http` + routage Go 1.22, `html/template`, `log/slog`, `embed`) ; une dépendance n'entre que si elle remplace beaucoup de code risqué.
- **Montants en centimes `int64`**, dates civiles en `YYYY-MM-DD` (pas d'heure, pas de fuseau dans les données).
- **Zéro artefact** : pas de code mort, pas de TODO laissé, pas de fichier généré commité (hors `go.sum` et assets vendorisés versionnés et documentés).

## 2. Choix techniques (détaillés dans `docs/DECISIONS.md`)

| Sujet | Choix |
|---|---|
| Go | 1.27 (1.24 n'est plus maintenu), `CGO_ENABLED=0`, binaire statique `-trimpath -ldflags="-s -w"` |
| SQLite | `modernc.org/sqlite`, WAL, `busy_timeout`, `synchronous=NORMAL`, 1 connexion écriture + pool lecture limité, `cache_size` borné |
| Migrations | migrateur embarqué maison (~60 lignes, `embed` + table `schema_migrations`), pas de goose |
| Routage | `http.ServeMux` (motifs Go 1.22), middlewares maison (session, logs sans données sensibles), CSRF par `http.CrossOriginProtection` |
| Front | `html/template` + HTMX + Alpine.js + uPlot (le plus léger), CSS maison à variables (clair/sombre), sans build Node |
| Mots de passe | argon2id (`x/crypto`), m=19 Mio, t=2, p=1 (minimum OWASP, adapté CPU faible) |
| Profils d'import | YAML embarqués, surchargeables dans `/data/profiles/` |
| XLSX / XLS | `excelize` (lecture en flux `Rows()`) / `extrame/xls` |
| Jours fériés FR | calculés (Pâques, etc.), sans dépendance |
| Images | réduction 1600 px + JPEG q75 (stdlib + `x/image/draw`) ; WebP non retenu (pas d'encodeur Go pur fiable) |
| PDF | `go-pdf/fpdf` |
| Chiffrement sauvegarde | `age` (passphrase/scrypt), format standard et outillé |
| Copie externe | dossier monté, WebDAV (HTTP stdlib), SFTP (`pkg/sftp`) |
| Healthcheck | sous-commande `buddyget healthcheck` (image `scratch` sans curl) |
| Tâche planifiée | un seul `time.Timer` réarmé chaque jour (alertes, sauvegarde, intérêts au 31/12), pas de polling |
| Licence | AGPL-3.0 |

## 3. Structure du dépôt

```
buddyget/
├── cmd/buddyget/            # main : config, DB, serveur, scheduler, sous-commande healthcheck
├── internal/
│   ├── config/              # variables d'environnement
│   ├── platform/
│   │   ├── db/              # ouverture SQLite, pragmas, transactions, migrateur
│   │   │   └── migrations/  # 0001_*.sql …
│   │   ├── money/           # centimes, parsing/formatage FR
│   │   └── calendar/        # jours ouvrés, fériés FR, périodicités, ajustement de dates
│   ├── auth/                # utilisateurs, sessions, argon2id, CSRF, limitation des tentatives
│   ├── ledger/              # comptes, opérations, virements, tags, pièces jointes, édition par lot, pointage
│   ├── categorize/          # catégories, règles, application (import + rétroactif)
│   ├── importer/            # lecteurs csv/xlsx/xls → profils → normalisation → dédup → prévisualisation → commit/annulation
│   │   └── profiles/        # sg.yaml, bnp.yaml, generique.yaml
│   ├── recurring/           # règles récurrentes, détection, générateur d'occurrences
│   ├── forecast/            # moteur de prévisionnel + calques de scénarios (moteur unique)
│   ├── savings/             # modèles de comptes, intérêts, blocage
│   ├── budget/              # enveloppes, report, reste à vivre, synthèses mensuelles
│   ├── alerts/              # évaluation des alertes + canaux (SMTP, webhook/ntfy)
│   ├── goals/  loans/  household/  reports/  exports/  backup/
│   ├── scheduler/           # tâche quotidienne unique
│   └── web/                 # serveur, middlewares, handlers par écran
│       ├── templates/       # layout, partials HTMX, pages
│       └── static/          # htmx, alpine, uplot (vendorisés), css, icônes, manifest PWA
├── testdata/                # sg/, bnp/, generique/ (anonymisés)
├── e2e/                     # Playwright (dev/CI uniquement) : 360/768/1280 px × clair/sombre
├── docs/                    # ARCHITECTURE.md, DECISIONS.md, PERFORMANCE.md, accessibilite/
├── .github/workflows/ci.yml # vet, golangci-lint, tests, buildx amd64+arm64, mesure taille image
├── Dockerfile  docker-compose.yml  Makefile  .golangci.yml  LICENSE  README.md
```

## 4. Jalons

| # | Contenu | Livrables vérifiables |
|---|---|---|
| 1 | Socle : config, SQLite + migrateur, auth (premier compte, sessions, CSRF, anti-bruteforce), gabarit UI responsive clair/sombre sans flash, `/healthz`, Dockerfile multi-arch, compose Synology, CI, LICENSE AGPL-3.0 | tests auth/migrations, RSS au repos mesurée, page de login à 360 px dans les 2 thèmes |
| 2 | Comptes, catégories (2 niveaux), saisie manuelle, virements liés, liste paginée + recherche FTS5 + filtres, édition par lot | parcours saisie < 15 s, cartes mobiles |
| 3 | Import CSV/XLSX/XLS : encodage/séparateur auto, profils SG/BNP/générique, mapping manuel + sauvegarde de profil, assistant 3 étapes, dédup par rang d'occurrence, historique + annulation, rapprochement du solde | tests parseurs/dédup, bench 10 000 lignes < 150 Mo |
| 4 | Règles de catégorisation (en 1 clic, rétroactives) et détection des virements internes | tests règles + rapprochement |
| 5 | Récurrences : détection (±3 j, ±10 %, score), écran de validation, création manuelle | tests détection |
| 6 | Prévisionnel : occurrences, jours non ouvrés, moyenne glissante, courbe jour par jour, point bas, seuil, exclusion ponctuelle | tests fin de mois/bissextile/fériés, bench 24 mois < 500 ms |
| 7 | Épargne : modèles, intérêts (simple, composé, quinzaine), blocage/disponible, versements programmés, vue Épargne | tests intérêts + blocage |
| 8 | Budgets/enveloppes, reste à vivre, alertes + canaux optionnels + tâche quotidienne | tests alertes |
| 9 | Abonnements, rapports, tags, pièces jointes, saisie rapide (modèles, dupliquer, voix), pointage rapide | |
| 10 | Scénarios « et si », projets/cagnottes, lissage annuel, crédits + amortissement | tests amortissement |
| 11 | Partage du foyer (qui doit combien), exports CSV/PDF/ZIP en flux, sauvegarde chiffrée + rotation + copie externe + restauration | test sauvegarde/restauration |
| 12 | Durcissement, Playwright complet, Lighthouse ≥ 90, `PERFORMANCE.md`, doc Synology pas à pas → **v1.0** | critères section 12 |
| 13 | Phase 2 (OFX/QIF/PDF, inflation, patrimoine) : interfaces prévues dès les jalons 3, 6 et 10 | hors v1 |

Chaque jalon se termine par : tests au vert, `go vet` + lint propres, README à jour, commit(s) Conventional Commits.

## 5. Contraintes de l'environnement de développement

- Environnement cloud : proxy.golang.org et le registre npm sont joignables ; Docker Hub et gnu.org ne le sont pas.
- Pas de démon Docker : l'image (taille, multi-arch, mémoire au repos) est construite et mesurée par la CI GitHub Actions ; la RAM et le démarrage sont mesurés sur le binaire en local.
- Conteneur éphémère : le code est poussé sur `CodeColors/buddyget` à chaque jalon.
