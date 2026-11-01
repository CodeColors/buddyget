# BuddyGet

Gestion du budget du foyer, auto-hébergée, pensée pour un NAS Synology peu
puissant : un seul conteneur, une base SQLite, moins de 20 Mo de RAM au repos.

- Import des relevés Société Générale et BNP Paribas (CSV, XLS, XLSX)
- Saisie manuelle rapide, sur ordinateur comme sur téléphone
- Budget prévisionnel à une date choisie, à partir des opérations récurrentes
- Comptes d'épargne avec intérêts et durée de blocage

Interface en français, modes clair et sombre, aucune requête vers l'extérieur
(sauf les notifications par e-mail ou webhook, si vous les activez).

### Disponible aujourd'hui

- Comptes (courant, épargne, espèces…) avec solde initial, archivage, IBAN masqué
- Catégories sur deux niveaux, avec un jeu par défaut modifiable
- Saisie d'une opération en quelques secondes (montant, libellé, Entrée), y
  compris les virements entre comptes du foyer
- Liste des opérations en cartes sur mobile, recherche instantanée
  (sans accents ni majuscules), filtres par compte, catégorie, période,
  montant et pointage, tri
- Recatégorisation ou changement de compte par lot, sur une sélection ou sur
  tous les résultats d'une recherche
- Import des relevés CSV, Excel (.xlsx) et Excel 97-2003 (.xls) en trois
  étapes (fichier, vérification, rapprochement) : encodage et séparateur
  détectés, format reconnu, choix des colonnes à la main sinon ; aucun
  doublon même en réimportant des fichiers qui se chevauchent ; historique
  et annulation d'un import
- Rapprochement : écart entre le solde de la banque et le solde calculé
- Règles de catégorisation : créées en un clic depuis une opération ou lors
  d'une recatégorisation par lot, appliquées aux imports (catégorie suggérée
  dès l'aperçu) et, à la demande, à l'historique
- Opérations récurrentes (loyer, salaire, abonnements…) détectées dans
  l'historique avec un score de confiance, à valider, modifier ou rejeter ;
  saisie manuelle avec toute périodicité, du hebdomadaire à l'annuel
- Virements entre vos comptes repérés dans les relevés (même montant, dates
  proches) et proposés au rapprochement, pour ne fausser ni les dépenses ni
  les revenus
- Prévisionnel à la date de votre choix (jusqu'à deux ans) : solde prévu,
  courbe jour par jour, point bas, périodes sous un seuil d'alerte, détail du
  calcul ligne par ligne ; échéances reportées au jour ouvré (week-ends et
  jours fériés), dépenses courantes estimées sur la moyenne des derniers mois,
  échéance écartée en un clic, solde pointé au choix
- Épargne : comptes créés à partir de modèles (Livret A, LDDS, LEP, PEL,
  assurance-vie…) aux taux et plafonds modifiables, intérêts calculés par
  quinzaine ou au jour le jour et inscrits au 31 décembre, fonds bloqués
  jusqu'à une date ou par versement, avertissement avant un retrait
  anticipé, versements programmés, évolution et projection
- Budgets par enveloppe (report facultatif d'un mois sur l'autre), reste à
  vivre du mois sur le tableau de bord, tendance sur douze mois
- Alertes (enveloppe, solde prévu sous le seuil, dépense inhabituelle,
  abonnement modifié, échéance proche, import oublié, déblocage d'épargne),
  avec cloche dans l'en-tête ; notifications par e-mail ou ntfy/webhook,
  désactivées par défaut et discrètes
- Saisie ultra-rapide : modèles en un clic, duplication, dictée vocale
  quand le navigateur la propose ; étiquettes libres (filtre, lot, rapport) ;
  photo ou PDF du ticket joint à l'opération, réduit automatiquement
- Pointage d'un geste face au solde de la banque, jusqu'à ce que le reste à
  pointer tombe à zéro
- Abonnements (coût annuel, doublons, abonnements peut-être oubliés) et
  rapports mensuel et annuel (catégories, taux d'épargne, commerçants)
- Crédits : tableau d'amortissement, capital restant dû, coût des intérêts
  et de l'assurance, remboursements anticipés ; la mensualité entre d'elle-même
  dans les récurrences et le prévisionnel
- Projets (vacances, travaux…) : cagnotte sur un compte d'épargne, versement
  mensuel suggéré et programmé en un clic, date d'atteinte, dépenses du
  projet par étiquette
- Dépenses annuelles lissées : provision mensuelle, mise de côté sur un
  compte d'épargne ou non, dans le prévisionnel et le reste à vivre
- Scénarios « et si » (achat, nouveau crédit, abonnement arrêté, hausse de
  salaire, remboursement anticipé…) comparés à la réalité, puis convertis en
  données réelles en un clic

## Avancement

| Jalon | Contenu | État |
|---|---|---|
| 1 | Socle : configuration, SQLite et migrations, authentification, gabarit d'interface, image Docker, CI | ✅ |
| 2 | Comptes, catégories, saisie manuelle, virements, liste et recherche, édition par lot | ✅ |
| 3 | Import CSV / XLSX / XLS, profils SG / BNP / générique, déduplication, annulation, rapprochement | ✅ (profils SG et BNP à vérifier sur des exports réels) |
| 4 | Règles de catégorisation (en un clic, rétroactives, à l'import), virements internes détectés | ✅ |
| 5 | Récurrences : détection avec score de confiance, validation, saisie manuelle | ✅ |
| 6 | Prévisionnel : courbe, point bas, seuil, jours non ouvrés, dépenses variables, exclusion ponctuelle | ✅ |
| 7 | Épargne : modèles, intérêts, disponible et bloqué, versements programmés, vue Épargne | ✅ |
| 8 | Budgets par enveloppe, reste à vivre, alertes et notifications facultatives | ✅ |
| 9 | Abonnements, rapports, étiquettes, pièces jointes, saisie rapide, pointage | ✅ |
| 10 | Scénarios « et si », projets et cagnottes, lissage des dépenses annuelles, crédits | ✅ |
| 11 – 12 | Partage, exports, sauvegardes, finitions | à venir |

Le détail est dans [docs/PLAN.md](docs/PLAN.md) et le cahier des charges dans
[docs/cahier-des-charges.md](docs/cahier-des-charges.md).

## Installation sur un NAS Synology

Prérequis : DSM 7.2 ou plus récent avec **Container Manager**.

### 1. Préparer le dossier de données

1. Dans **File Station**, créez le dossier `docker/buddyget` (soit
   `/volume1/docker/buddyget`).
2. Le conteneur écrit dans ce dossier avec l'identité indiquée par la ligne
   `user` du fichier `docker-compose.yml` (`1026:100` : premier compte
   administrateur créé sur le NAS, groupe `users`). Pour un autre propriétaire,
   connectez-vous en SSH et lancez `id <utilisateur>` pour obtenir ses UID et
   GID.

### 2. Donner accès à l'image

L'image est publiée sur `ghcr.io/codecolors/buddyget`. Tant que le dépôt est
privé, le NAS doit s'authentifier :

1. Sur GitHub, créez un jeton (*Settings → Developer settings → Personal access
   tokens*) avec la seule permission `read:packages`.
2. Dans **Container Manager → Registre → Paramètres → Ajouter**, saisissez
   l'URL `https://ghcr.io`, votre identifiant GitHub et ce jeton.

L'étiquette `latest` désigne la dernière version publiée ; `main` suit la
version en cours de développement.

### 3. Créer le projet

1. **Container Manager → Projet → Créer**.
2. Nom : `buddyget` ; chemin : `/docker/buddyget` ; source : *Créer un fichier
   docker-compose.yml*.
3. Collez le contenu de [docker-compose.yml](docker-compose.yml), ajustez
   `user` et le port si besoin, puis validez : le conteneur démarre.
4. Ouvrez `http://<adresse-du-nas>:8080` : BuddyGet propose de créer le
   premier compte.

### 4. Accès en HTTPS (recommandé)

1. **Panneau de configuration → Portail de connexion → Avancé → Proxy
   inversé → Créer** : source `HTTPS`, nom d'hôte `budget.mondomaine.fr`,
   port 443 ; destination `HTTP`, `localhost`, port 8080.
2. Dans `docker-compose.yml`, décommentez et renseignez :
   - `BASE_URL: https://budget.mondomaine.fr` (cookies `Secure`, origine admise) ;
   - `TRUSTED_PROXIES: 172.16.0.0/12`, pour que l'adresse réelle des visiteurs
     (en-tête `X-Forwarded-For`) serve à la limitation des tentatives de
     connexion. Si les journaux n'affichent que l'adresse du proxy, ajoutez
     dans l'onglet *En-tête personnalisé* du proxy inversé l'en-tête
     `X-Forwarded-For` avec la valeur `$proxy_add_x_forwarded_for`.
3. Recréez le projet pour appliquer les changements.

### Mise à jour

1. **Container Manager → Projet → buddyget → Action → Arrêter**.
2. **Container Manager → Image** : sélectionnez `ghcr.io/codecolors/buddyget`
   puis **Mettre à jour**.
3. **Projet → buddyget → Action → Construire** recrée le conteneur ; les
   migrations de la base s'appliquent au démarrage.

### Sauvegarde

Les données tiennent dans le dossier `/volume1/docker/buddyget` (fichier
`buddyget.db` et sous-dossier `attachments` des pièces jointes). Arrêtez le projet avant de copier ce dossier, par exemple avec
**Hyper Backup**. La sauvegarde intégrée (chiffrée, planifiée) arrive au
jalon 11.

## Ajouter ou ajuster un profil d'import

Un profil décrit un format d'export : pour chaque donnée, les intitulés de
colonne possibles (casse et accents ignorés). Le plus simple est de passer
par l'assistant : à l'étape « Colonnes du fichier », indiquez la ligne
d'en-tête et les colonnes, puis donnez un nom au profil ; il est enregistré
dans `/data/profiles/` et reconnu aux imports suivants.

À la main, déposez un fichier `.yaml` dans `/volume1/docker/buddyget/profiles/`.
Un fichier nommé `sg.yaml`, `bnp.yaml` ou `generique.yaml` remplace le profil
fourni du même nom ; tout autre fichier ajoute un profil, essayé en premier.
Les modifications s'appliquent au dépôt suivant, sans redémarrage.

```yaml
nom: Ma banque
colonnes:
  date: ["Date opération", "Date"]
  libelle: ["Libellé détaillé", "Libellé"]  # la première colonne non vide l'emporte
  montant: ["Montant"]                       # ou bien :
  # debit: ["Débit"]
  # credit: ["Crédit"]
format_date: JJ/MM/AAAA   # facultatif : JJ/MM/AA, AAAA-MM-JJ, JJ-MM-AAAA, JJ.MM.AAAA, AAAA/MM/JJ
# montants_inverses: true  # si les dépenses sont écrites en positif
```

Les profils Société Générale et BNP Paribas fournis sont **provisoires** :
ils ont été établis sans export réel. Si votre export n'est pas reconnu,
l'assistant propose le choix des colonnes ; signalez-le pour que le profil
soit corrigé.

## Configuration

Toutes les options sont des variables d'environnement facultatives.

| Variable | Défaut | Rôle |
|---|---|---|
| `PORT` | `8080` | port d'écoute HTTP |
| `DATA_DIR` | `/data` | dossier de la base de données |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` ou `error` |
| `TZ` | UTC | fuseau des journaux (`Europe/Paris`) |
| `BASE_URL` | — | adresse publique, ex. `https://budget.mondomaine.fr` ; en HTTPS, active les cookies `Secure` |
| `TRUSTED_PROXIES` | — | adresses ou réseaux CIDR (séparés par des virgules) dont l'en-tête `X-Forwarded-For` est cru |
| `MAX_UPLOAD_MB` | `10` | taille maximale d'un fichier déposé : relevé importé ou pièce jointe (1 à 100) |
| `ATTACHMENTS_QUOTA_MB` | `1024` | place totale des pièces jointes (10 à 1 000 000) |
| `GOMEMLIMIT` | `100MiB` (image) | plafond mémoire visé par le ramasse-miettes |

Les journaux ne contiennent jamais de libellé ni de montant.

## Développement

Prérequis : Go 1.27, et pour les tests d'interface Node.js 22.

```bash
make run    # compile et lance sur http://localhost:8080, données dans ./data
make test   # tests unitaires et d'intégration (avec -race)
make lint   # go vet + golangci-lint
make e2e    # parcours Playwright à 360, 768 et 1280 px, thèmes clair et sombre
```

Les captures des tests d'interface sont écrites dans `e2e/screenshots/` et
publiées comme artefact par la CI. L'architecture est décrite dans
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), les choix techniques dans
[docs/DECISIONS.md](docs/DECISIONS.md) et les mesures dans
[docs/PERFORMANCE.md](docs/PERFORMANCE.md).

## Licence

[GNU AGPL-3.0](LICENSE).
