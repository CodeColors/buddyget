# BuddyGet

Gestion du budget du foyer, auto-hébergée, pensée pour un NAS Synology peu
puissant : un seul conteneur, une base SQLite, moins de 20 Mo de RAM au repos.

- Import des relevés Société Générale et BNP Paribas (CSV, XLS, XLSX)
- Saisie manuelle rapide, sur ordinateur comme sur téléphone
- Budget prévisionnel à une date choisie, à partir des opérations récurrentes
- Comptes d'épargne avec intérêts et durée de blocage

Interface en français, modes clair et sombre, aucune requête vers l'extérieur.

## Avancement

| Jalon | Contenu | État |
|---|---|---|
| 1 | Socle : configuration, SQLite et migrations, authentification, gabarit d'interface, image Docker, CI | ✅ |
| 2 | Comptes, catégories, saisie manuelle, liste et recherche | à venir |
| 3 | Import CSV / XLSX / XLS | à venir |
| 4 – 12 | Règles, récurrences, prévisionnel, épargne, budgets, alertes, rapports, exports, sauvegardes | à venir |

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
`buddyget.db`). Arrêtez le projet avant de copier ce dossier, par exemple avec
**Hyper Backup**. La sauvegarde intégrée (chiffrée, planifiée) arrive au
jalon 11.

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
