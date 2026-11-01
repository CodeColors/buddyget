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
cmd/buddyget/          point d'entrée : configuration, restauration en attente, base,
                       serveur, tâche quotidienne, arrêt propre ; sous-commandes
                       healthcheck et reset-password
internal/
├── config/            variables d'environnement → Config validée
├── platform/
│   ├── db/            ouverture SQLite (WAL, pools écriture/lecture), transactions,
│   │                  lecture de lignes (Collect), instantané et contrôle
│   │                  d'une base (Snapshot, Verify),
│   │   └── migrations/ migrateur ; NNNN_description.sql embarqués
│   ├── money/         montants en centimes : saisie et affichage à la française
│   ├── calendar/      dates civiles AAAA-MM-JJ, périodicités et échéances,
│   │                  jours fériés et report au jour ouvré
│   ├── text/          forme de comparaison des textes (casse, accents, espaces)
│   └── validation/    erreur de saisie commune (message destiné à l'utilisateur)
├── auth/              utilisateurs, argon2id, limitation des tentatives, sessions,
│                      thème, changement et réinitialisation du mot de passe
├── ledger/            grand livre : comptes, catégories, opérations, virements,
│                      recherche (Filter), édition par lot, étiquettes, modèles
│                      de saisie, rapprochement et pointage, virements internes
├── categorize/        règles de catégorisation : moteur, application rétroactive
├── recurring/         récurrences : règles, échéances (date théorique et effective),
│                      détection avec score
├── forecast/          prévisionnel : moteur pur (Compute), service (réglages,
│                      exclusions, dépenses variables, intérêts estimés,
│                      lissage, scénarios et comparaison, reste à vivre)
├── savings/           épargne : modèles, réglages, intérêts (quinzaine, simple,
│                      composé), disponible/bloqué, inscription des intérêts
├── budget/            enveloppes mensuelles : report, consommé, tendance
├── loans/             crédits : tableau d'amortissement, remboursements
│                      anticipés, récurrence de la mensualité
├── goals/             projets : cagnotte, versement suggéré, date d'atteinte
├── scenarios/         scénarios « et si » : modifications, conversion en
│                      données réelles
├── household/         partage du foyer : membres, répartitions, soldes,
│                      remboursements
├── exports/           exports en flux : opérations CSV, rapports CSV/PDF,
│                      dossier ZIP
├── backup/            sauvegardes : archive chiffrée (age), rotation, copie
│                      dossier/WebDAV, restauration au démarrage
├── reports/           rapports mensuel et annuel
├── attachments/       pièces jointes : réduction des images, PDF, quota
├── alerts/            alertes : examen (7 types), acquittement, notifications
│                      SMTP et webhook
├── scheduler/         tâche quotidienne unique (un minuteur réarmé chaque jour)
├── importer/          import : lecteurs CSV/XLSX/XLS, profils, Mapping, déduplication,
│   │                  brouillon, validation, historique, annulation
│   └── profiles/      sg.yaml, bnp.yaml, generique.yaml (embarqués)
└── web/               routage, middlewares, handlers par écran, rendu
    ├── templates/     layout.html + pages/<page>.html
    └── static/        CSS, app.js, HTMX (vendorisé), icône, manifeste PWA
e2e/                   tests Playwright (développement et CI uniquement)
testdata/              relevés d'exemple (synthétiques, voir testdata/README.md)
```

Chaque domaine est un service, appelé par les handlers. Le prévisionnel
lit les autres domaines par ses dépendances (`forecast.Deps`) ; aucun
domaine ne dépend du prévisionnel.

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
| `recurring_rules` | récurrences : libellé, compte (et compte destinataire d'un versement programmé), montant (fixe ou estimé), périodicité, report au jour ouvré, première et dernière échéance, catégorie, origine, active, abonnement, lissage (et compte de lissage), signature de la série |
| `recurring_dismissals` | séries rejetées à la détection |
| `forecast_exclusions` | échéances écartées du prévisionnel (récurrence, date théorique) |
| `forecast_settings` | réglages du prévisionnel (une ligne) : seuil, solde pointé, moyenne des dépenses variables |
| `savings_settings` | réglages d'un compte d'épargne : produit, taux, méthode d'intérêts, inscription automatique, plafond, blocage (date ou durée), pénalité |
| `budgets` | enveloppes : catégorie, plafond mensuel, report, mois de départ |
| `alerts` | alertes : type, clé de la situation, titre, détail, lien, date, acquittement, notification |
| `alert_settings` | réglage de chaque type d'alerte : activé, paramètre |
| `notification_settings` | canaux sortants (une ligne) : SMTP, webhook, rythme, discrétion |
| `tags`, `transaction_tags` | étiquettes et leurs opérations |
| `attachments` | pièces jointes : opération, nom d'origine, nature (image ou PDF), taille, fichier |
| `quick_templates` | modèles de saisie : nom, compte, libellé, montant, catégorie |
| `loans`, `loan_prepayments` | crédits : compte prélevé, capital, taux, durée, assurance, première échéance, récurrence de la mensualité ; remboursements anticipés |
| `goals` | projets : montant et date visés, compte d'épargne, cagnotte, étiquette, versement programmé |
| `household_members` | membres du foyer : prénom, revenu mensuel, archivage |
| `shared_transactions`, `transaction_splits` | opération partagée : payeur, règle de répartition ; part de chaque membre |
| `member_settlements` | remboursements entre membres, avec leur virement interne |
| `backup_settings` | réglages des sauvegardes (une ligne) : automatique, rotation, copie externe, compte rendu |
| `scenarios`, `scenario_items` | scénarios et leurs modifications (nature, compte, récurrence ou crédit visé, montant, pourcentage, taux, durée, dates) |

Une opération importée porte `import_id` (annulation) et `import_hash`
(empreinte unique par compte, garde-fou contre les doublons). Une écriture
d'intérêts porte `interest_year` (une par compte et par année). Le compte porte
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
| `/comptes/{id}/rapprochement` | étape 3 de l'import, et vue de pointage : solde de la banque, solde pointé, reste à pointer, opérations à pointer (`POST /operations/{id}/pointer`, HTMX) |
| `/import/historique` | imports validés, annulation |
| `/regles`, `/regles/nouvelle`, `/regles/{id}` | règles de catégorisation, application aux opérations sans catégorie |
| `/operations/virements` | virements internes possibles : relier ou écarter |
| `/recurrences`, `/recurrences/nouvelle`, `/recurrences/{id}` | récurrences : liste et prochaines échéances, saisie, suspension |
| `/recurrences/detectees` | séries détectées : valider, modifier, rejeter |
| `/previsionnel` | prévisionnel à une date cible (`?compte=&date=`) : solde prévu, point bas, courbe, détail, échéances ; exclusion (`POST /previsionnel/exclusions`) et réglages (`POST /previsionnel/reglages`) |
| `/epargne` | épargne : total, disponible et bloqué, intérêts, évolution, versements programmés, projection (`?date=`) ; inscription des intérêts d'une année close (`POST /epargne/{id}/interets`) |
| `/budgets`, `/budgets/nouveau`, `/budgets/{catégorie}` | enveloppes du mois (`?mois=AAAA-MM`), tendance sur 12 mois, saisie et suppression |
| `/alertes`, `/alertes/reglages` | alertes à lire et acquittement ; types d'alerte, canaux sortants, notification d'essai |
| `/operations/nouvelle?dupliquer=` | saisie préremplie d'après une opération ou la dernière saisie ; modèles (`POST /operations/{id}/modele`, `POST /modeles/{id}/utiliser`) |
| `/operations/{id}/pieces`, `/pieces/{id}` | pièces jointes : envoi, affichage ou téléchargement, suppression |
| `/etiquettes` | étiquettes : nombre d'opérations, renommage, retrait |
| `/abonnements` | abonnements : coûts mensuel et annuel, doublons possibles, abonnements peut-être oubliés |
| `/rapports` | rapport mensuel (`?mois=AAAA-MM`) ou annuel (`?annee=AAAA`) |
| `/credits`, `/credits/nouveau`, `/credits/{id}` | crédits : situation, tableau d'amortissement par année, remboursement anticipé (`POST /credits/{id}/anticipe`), modification (`/credits/{id}/modifier`) |
| `/projets`, `/projets/nouveau`, `/projets/{id}` | projets : progression, cagnotte (`POST /projets/{id}/cagnotte`), versement programmé (`POST /projets/{id}/versement`), saisie |
| `/scenarios`, `/scenarios/{id}` | scénarios : création, comparaison (`?date=`), modifications (`POST /scenarios/{id}/modifications`), conversion (`POST /scenarios/{id}/convertir`) ; le prévisionnel applique un scénario avec `?scenario=` |
| `/partage`, `/partage/membres/{id}` | partage du foyer : qui doit combien, opérations partagées, remboursements (`POST /partage/remboursements`), membres |
| `/exports` | exports : `/exports/operations` (CSV, critères de `/operations`), `/exports/rapport` (`?mois=` ou `?annee=`, `&format=csv`), `/exports/dossier` (ZIP, `&pieces=1`) |
| `/sauvegardes` | sauvegardes : maintenant (`POST`), réglages, téléchargement (`/sauvegardes/fichiers/{nom}`), restauration (`POST /sauvegardes/restaurer`) |
| `/parametres` | réglages : comptes, utilisateurs, membres, notifications, sauvegardes, catégories, règles, étiquettes |
| `/utilisateurs` | comptes de connexion : ajout, changement de mot de passe (`POST /utilisateurs/mot-de-passe`), suppression |
| `/plus` | écrans secondaires, par usage : suivre, prévoir, foyer et données |

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
- Graphiques en SVG calculé côté serveur (`chart.go`), stylés par classes CSS.
- Amélioration progressive : chaque écran fonctionne sans JavaScript ; HTMX
  rend la recherche instantanée, `app.js` ajoute confirmations et « tout cocher ».
