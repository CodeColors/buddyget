# Décisions techniques

Chaque décision indique son contexte et sa raison. Une décision remplacée est
mise à jour sur place (l'historique reste dans Git).

## Socle (jalon 1)

### Go 1.27
Le plan prévoyait Go 1.24, qui n'est plus maintenu (seules les deux dernières
versions le sont). Les versions courantes de `modernc.org/sqlite` et de
`golang.org/x/crypto` exigent Go ≥ 1.26. Go 1.27 est la version stable
actuelle ; `go.mod` déclare `go 1.27.0`, le Dockerfile et la CI suivent.

### SQLite : `modernc.org/sqlite`, une connexion d'écriture, un petit pool de lecture
- Pilote pur Go : binaire statique, `CGO_ENABLED=0`, compilation croisée triviale.
- Mode WAL, `synchronous=NORMAL` (durable à chaque point de contrôle, sûr en
  WAL), `busy_timeout=5000`, `foreign_keys=1`, `cache_size` borné à 2 Mio par
  connexion.
- Une seule connexion d'écriture (SQLite sérialise de toute façon les
  écritures) avec `_txlock=immediate` : une transaction prend le verrou dès
  `BEGIN`, sans échec tardif. Quatre connexions de lecture au plus, en
  `query_only`, fermées après 5 minutes d'inactivité pour libérer leur cache.

### Migrations : migrateur embarqué (≈ 80 lignes)
Fichiers `internal/platform/db/migrations/NNNN_description.sql` embarqués,
table `schema_migrations`, une transaction par migration. Les numéros doivent
se suivre depuis 1 (un trou ou un nom invalide arrête le démarrage), et une
base plus récente que le programme est refusée plutôt que d'être utilisée à
l'aveugle. `goose` apporterait des fonctions inutiles ici.

### Protection CSRF : `http.CrossOriginProtection` (bibliothèque standard)
Le plan prévoyait un middleware CSRF maison à jetons. Depuis Go 1.25, la
bibliothèque standard refuse toute requête non sûre (POST…) émise depuis une
autre origine, d'après les en-têtes `Sec-Fetch-Site` ou `Origin` envoyés par
tous les navigateurs actuels. Avantages : aucun jeton à générer, stocker ni
glisser dans chaque formulaire (et plus tard dans chaque requête HTMX), y
compris sur la page de connexion. Les cookies `SameSite=Lax` complètent le
dispositif. Si `BASE_URL` est définie, son origine est explicitement admise
(cas d'un reverse proxy qui réécrirait l'en-tête `Host`).

### Sessions
- Jeton opaque de 128 bits (`crypto/rand.Text`) dans un cookie `HttpOnly`,
  `SameSite=Lax`, `Secure` dès que `BASE_URL` est en HTTPS (le cookie reste
  utilisable en HTTP simple sur le réseau local).
- Seule l'empreinte SHA-256 du jeton est stockée : une copie de la base ne
  permet pas de reprendre une session.
- Durée de vie glissante de 30 jours, prolongée seulement quand il en reste
  moins de la moitié : pas d'écriture en base à chaque requête.
- Les sessions expirées sont purgées à chaque connexion : aucune tâche de fond.

### Mots de passe : argon2id 19 Mio / 2 passes / 1 fil
- Minimum recommandé par l'OWASP, adapté à un CPU de NAS ; format PHC, dont
  les paramètres sont relus à la vérification (ils pourront évoluer sans
  invalider les comptes).
- Deux calculs simultanés au plus : 10 connexions simultanées restent sous
  60 Mo (voir `PERFORMANCE.md`).
- Le tampon de 19 Mio est rendu au système après chaque calcul
  (`debug.FreeOSMemory`) : sans cela, faute d'allocations au repos, il restait
  dans le tas plusieurs minutes et la mémoire au repos passait de 16 à 55 Mo.
  Coût mesuré : environ 1 ms par connexion.
- Mot de passe de 12 caractères au minimum (256 au maximum).
- Un identifiant inconnu déclenche quand même une vérification complète :
  la durée de réponse ne révèle pas quels comptes existent.

### Anti-bruteforce
Au-delà de 5 échecs en 15 minutes pour une même adresse IP **ou** un même
identifiant, les tentatives sont refusées (HTTP 429 et `Retry-After`) jusqu'à
la fin de la fenêtre. Le compteur vit en mémoire, borné à 10 000 entrées, sans
tâche de fond ; il repart de zéro au redémarrage, ce qui est acceptable. Le
blocage par identifiant peut gêner temporairement son titulaire si quelqu'un
d'autre s'acharne sur son compte : compromis assumé pour un foyer, où la
protection du compte prime. L'adresse IP réelle n'est lue dans
`X-Forwarded-For` que pour les proxys déclarés dans `TRUSTED_PROXIES`.

### Thèmes clair et sombre sans script
Le thème choisi (Auto / Clair / Sombre) est stocké par utilisateur et rendu
par le serveur dans `<html data-theme="…">` : la bonne palette s'applique dès
le premier octet, sans flash ni JavaScript. Chaque couleur est définie une
seule fois avec `light-dark()`, la propriété `color-scheme` choisissant la
valeur (préférence de l'appareil en mode Auto). Les pages anonymes (connexion)
suivent l'appareil. Le sélecteur est un formulaire de trois boutons
(`aria-pressed`), utilisable sans script. Contrastes vérifiés WCAG AA dans les
deux thèmes.

### Front-end : rien d'inutile embarqué
HTMX, Alpine.js et uPlot seront vendorisés au jalon où ils serviront pour la
première fois (jalon 2 pour HTMX) : le jalon 1 n'en a pas besoin. La politique
CSP interdit tout script ou style en ligne et toute ressource externe.

Les fichiers statiques sont embarqués, servis depuis la mémoire, et leur URL
porte l'empreinte du contenu (`/static/app.css?v=…`) : cache navigateur d'un
an, invalidé automatiquement à chaque nouvelle version.

### PWA : icône SVG unique
Le manifeste déclare une icône SVG (`sizes: any`), reconnue par les
navigateurs Chromium et Firefox. Aucune déclinaison PNG générée n'est
versionnée ; iOS utilise alors une capture de la page comme icône d'écran
d'accueil.

### Journaux
`log/slog` au format texte `clé=valeur` sur la sortie standard, lisible
directement dans Container Manager. Les requêtes HTTP ne sont journalisées
qu'en niveau `debug`, chemin seul : ni chaîne de requête, ni corps, ni
libellé, ni montant. La base des fuseaux horaires est embarquée (`time/tzdata`,
≈ 450 Ko) pour que `TZ=Europe/Paris` s'applique dans l'image `scratch`.

### Image Docker
- Compilation croisée dans l'étape de build (`--platform=$BUILDPLATFORM`) :
  amd64 et arm64 sans émulation QEMU.
- Image finale `scratch` : le binaire seul, utilisateur non-root `65532`,
  `GOMEMLIMIT=100MiB` (marge sous la limite `mem_limit: 128m` du compose).
- `HEALTHCHECK` par la sous-commande `buddyget healthcheck` (pas de shell ni
  de `curl` dans l'image).
- Publication sur `ghcr.io/codecolors/buddyget` par la CI : étiquette `main`
  pour la branche principale, `X.Y.Z`, `X.Y` et `latest` pour les tags `vX.Y.Z`.

### Tests de bout en bout
Bibliothèque `playwright` pilotée par le lanceur de tests intégré de Node
(`node --test`) : une seule dépendance de développement, jamais embarquée dans
l'image. Les tests démarrent le binaire sur une base vierge.

### Licence : AGPL-3.0
Logiciel auto-hébergé : l'AGPL garantit que toute version modifiée proposée
en service réseau reste ouverte.
