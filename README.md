# BuddyGet

Gestion du budget du foyer, auto-hébergée, pensée pour un NAS Synology peu
puissant : un seul conteneur de 21 Mo, une base SQLite, une trentaine de Mo
de RAM au repos.

- Import des relevés Société Générale et BNP Paribas (CSV, XLS, XLSX)
- Saisie manuelle rapide, sur ordinateur comme sur téléphone
- Budget prévisionnel à une date choisie, à partir des opérations récurrentes
- Comptes d'épargne avec intérêts et durée de blocage

Interface en français, modes clair et sombre, aucune requête vers l'extérieur
(sauf les notifications par e-mail ou webhook et la copie des sauvegardes
vers un serveur WebDAV, si vous les activez).

### Fonctionnalités

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
- Partage du foyer : qui a payé, répartition à parts égales, au prorata des
  revenus ou libre, qui doit combien, remboursement enregistré comme virement
  entre vos comptes
- Exports : opérations en CSV (aussi depuis une recherche), rapports en PDF
  ou CSV, dossier ZIP pour un comptable ou les impôts avec les pièces jointes
- Sauvegardes chiffrées, automatiques chaque nuit avec rotation, copie vers
  un dossier ou un serveur WebDAV, restauration en quelques clics
- Paramètres : comptes de connexion pour chaque personne du foyer,
  changement de mot de passe, et recours en ligne de commande en cas d'oubli

## Avancement

| Jalon | Contenu | État |
|---|---|---|
| 1 | Socle : configuration, SQLite et migrations, authentification, gabarit d'interface, image Docker, CI | ✅ |
| 2 | Comptes, catégories, saisie manuelle, virements, liste et recherche, édition par lot | ✅ |
| 3 | Import CSV / XLSX / XLS, profils SG / BNP / générique, déduplication, annulation, rapprochement | ✅ |
| 4 | Règles de catégorisation (en un clic, rétroactives, à l'import), virements internes détectés | ✅ |
| 5 | Récurrences : détection avec score de confiance, validation, saisie manuelle | ✅ |
| 6 | Prévisionnel : courbe, point bas, seuil, jours non ouvrés, dépenses variables, exclusion ponctuelle | ✅ |
| 7 | Épargne : modèles, intérêts, disponible et bloqué, versements programmés, vue Épargne | ✅ |
| 8 | Budgets par enveloppe, reste à vivre, alertes et notifications facultatives | ✅ |
| 9 | Abonnements, rapports, étiquettes, pièces jointes, saisie rapide, pointage | ✅ |
| 10 | Scénarios « et si », projets et cagnottes, lissage des dépenses annuelles, crédits | ✅ |
| 11 | Partage du foyer, exports CSV / PDF / ZIP, sauvegardes chiffrées, copie externe, restauration | ✅ |
| 12 | Durcissement, paramètres et utilisateurs, accessibilité, documentation finale | ✅ **v1.0** |

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

### 5. Premiers pas

1. Créez le premier compte de connexion (identifiant et mot de passe de
   12 caractères au moins), puis, dans **Plus → Paramètres → Utilisateurs**,
   un compte pour chaque personne du foyer.
2. **Comptes** : ajoutez vos comptes courants et d'épargne avec leur solde à
   une date (le solde du relevé, par exemple) ; les modèles Livret A, LDDS,
   LEP, PEL… préremplissent taux et plafonds.
3. **Importer** : déposez l'export de votre banque (CSV, XLS ou XLSX) ;
   vérifiez l'aperçu, validez, puis rapprochez le solde avec celui de la
   banque.
4. **Plus → Récurrences** : validez les séries détectées (loyer, salaire,
   abonnements) ; le **Prévisionnel** est alors complet.
5. **Plus → Paramètres → Sauvegardes** : activez la sauvegarde de nuit et,
   idéalement, `BACKUP_PASSPHRASE` et une copie hors du NAS.

Sur le téléphone, ouvrez l'adresse dans le navigateur puis « Ajouter à
l'écran d'accueil » : BuddyGet s'ouvre comme une application.

### Mise à jour

1. **Container Manager → Projet → buddyget → Action → Arrêter**.
2. **Container Manager → Image** : sélectionnez `ghcr.io/codecolors/buddyget`
   puis **Mettre à jour**.
3. **Projet → buddyget → Action → Construire** recrée le conteneur ; les
   migrations de la base s'appliquent au démarrage.

### Sauvegarde et restauration

Écran **Plus → Sauvegardes** :

- **Sauvegarder maintenant** crée une archive cohérente de la base et des
  pièces jointes dans `/volume1/docker/buddyget/backups/`, sans arrêter
  BuddyGet ; chaque archive se télécharge depuis la liste.
- **Sauvegarder automatiquement chaque nuit** (vers 00h05) garde les N
  dernières sauvegardes automatiques (7 par défaut) ; celles faites à la main
  ne sont jamais supprimées.
- **Chiffrement** : ajoutez `BACKUP_PASSPHRASE` (12 caractères au moins) dans
  `environment` de `docker-compose.yml`, puis recréez le projet. Les archives
  (`.tar.gz.age`) sont alors chiffrées au format [age](https://age-encryption.org) :
  sans la phrase de passe, elles sont illisibles. **Conservez-la hors du
  NAS** (gestionnaire de mots de passe). Elle n'est jamais enregistrée dans
  la base.
- **Copie vers un second emplacement** (désactivée par défaut) :
  - *Dossier* : montez un autre dossier dans le conteneur (par exemple un
    autre volume, un disque USB ou un dossier synchronisé par Cloud Sync),
    en ajoutant à `volumes` une ligne `- /volumeUSB1/usbshare/buddyget:/copie`,
    puis indiquez `/copie` ; la même rotation s'y applique.
  - *WebDAV* : l'adresse d'un dossier WebDAV (Nextcloud, serveur WebDAV de
    DSM d'un autre NAS…), avec identifiant et mot de passe ; la rotation y
    reste à la charge du serveur.

**Restaurer** : bouton *Restaurer* d'une archive de la liste, ou dépôt d'un
fichier téléchargé. L'archive est déchiffrée et vérifiée (intégrité de la
base, version), puis BuddyGet redémarre et l'applique avant d'ouvrir la
base ; la page se recharge d'elle-même. Les données remplacées sont mises de
côté dans `/volume1/docker/buddyget/avant-restauration/` (à supprimer une
fois la restauration vérifiée). Le redémarrage repose sur
`restart: unless-stopped` ; sinon, redémarrez le projet.

**Sur un nouveau NAS** : installez BuddyGet (même `BACKUP_PASSPHRASE` si
l'archive est chiffrée), créez un premier compte, puis restaurez l'archive
depuis l'écran Sauvegardes ; les comptes de connexion de la sauvegarde
remplacent le compte provisoire.

À la main, sans BuddyGet : `age -d buddyget-….tar.gz.age | tar xz` (ou
`tar xzf` pour une archive non chiffrée) donne `buddyget.db` et le dossier
`attachments`, à replacer dans le dossier de données, BuddyGet arrêté.

### Mot de passe oublié

En SSH sur le NAS (**Panneau de configuration → Terminal et SNMP → Activer
le service SSH**), puis :

```bash
sudo docker exec buddyget /buddyget reset-password <identifiant>
```

La commande affiche un nouveau mot de passe aléatoire et ferme les sessions
de ce compte ; changez-le après connexion (**Plus → Paramètres →
Utilisateurs**).

### Dépannage

| Symptôme | Cause probable et solution |
|---|---|
| Le conteneur s'arrête aussitôt, journal « permission denied » sur `/data` | Le dossier n'appartient pas à l'UID:GID de la ligne `user` : corrigez la ligne (`id <utilisateur>` en SSH) ou les droits du dossier dans File Station. |
| Le port 8080 est déjà utilisé | Changez la partie gauche de `ports` (par exemple `8090:8080`) et l'adresse du proxy inversé. |
| « Accès refusé » à l'image au téléchargement | Le registre `ghcr.io` n'est pas configuré ou le jeton a expiré (étape 2). |
| La connexion échoue derrière le proxy inversé | `BASE_URL` doit correspondre exactement à l'adresse publique (schéma `https://` compris). |
| Trop de tentatives de connexion | Après 5 échecs, les essais sont suspendus 15 minutes pour cette adresse et cet identifiant. |
| Les dates ou la tâche de nuit sont décalées | `TZ: Europe/Paris` manque dans `environment`. |
| Une sauvegarde chiffrée ne se restaure pas | La phrase de passe doit être la même que lors de la sauvegarde ; l'écran Sauvegardes affiche aussi le dernier échec. |

Les journaux sont dans **Container Manager → Conteneur → buddyget →
Journal** ; ils ne contiennent ni libellé ni montant. `LOG_LEVEL: debug`
ajoute le détail des requêtes.

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

Les profils Société Générale et BNP Paribas sont vérifiés sur des exports
réels (.xls, en-tête en ligne 3). Si votre export n'est pas reconnu,
l'assistant propose le choix des colonnes ; signalez-le pour que le profil
soit corrigé.

## Configuration

Toutes les options sont des variables d'environnement facultatives.

| Variable | Défaut | Rôle |
|---|---|---|
| `PORT` | `8080` | port d'écoute HTTP |
| `DATA_DIR` | `/data` | dossier de la base de données |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` ou `error` |
| `TZ` | UTC | fuseau horaire : date du jour, tâche de nuit, journaux (`Europe/Paris`) |
| `BASE_URL` | — | adresse publique, ex. `https://budget.mondomaine.fr` ; en HTTPS, active les cookies `Secure` |
| `TRUSTED_PROXIES` | — | adresses ou réseaux CIDR (séparés par des virgules) dont l'en-tête `X-Forwarded-For` est cru |
| `MAX_UPLOAD_MB` | `10` | taille maximale d'un fichier déposé : relevé importé ou pièce jointe (1 à 100) |
| `ATTACHMENTS_QUOTA_MB` | `1024` | place totale des pièces jointes (10 à 1 000 000) |
| `BACKUP_PASSPHRASE` | — | phrase de passe (12 caractères au moins) qui chiffre les sauvegardes ; sans elle, elles ne le sont pas |
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
[docs/DECISIONS.md](docs/DECISIONS.md), les mesures dans
[docs/PERFORMANCE.md](docs/PERFORMANCE.md), l'accessibilité dans
[docs/ACCESSIBILITE.md](docs/ACCESSIBILITE.md) et la vérification des
critères d'acceptation dans [docs/RECETTE.md](docs/RECETTE.md).

## Licence

[GNU AGPL-3.0](LICENSE).
