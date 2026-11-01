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
Une bibliothèque n'est vendorisée qu'au jalon où elle sert pour la première
fois (HTMX au jalon 2). La courbe du prévisionnel est un SVG rendu côté
serveur : uPlot, prévu au plan, n'a pas été nécessaire (voir le jalon 6). La
politique CSP interdit tout script ou style en ligne et toute ressource
externe.

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

## Grand livre (jalon 2)

### Montants
Centimes `int64` partout (`internal/platform/money`). La saisie accepte les
écritures courantes : « 12,50 », « 12.50 », « 12€50 », « 1 234,56 »,
« 1.234,56 » ; deux décimales au plus, sinon l'erreur est explicite.
L'affichage suit la typographie française : espace fine insécable entre les
milliers, espace insécable avant « € », vrai signe moins. Le signe est
toujours écrit : la couleur (vert pour les entrées) ne fait que le souligner.

### Comptes
- Le solde initial est le solde **au début** du jour indiqué : une opération
  saisie ce jour-là le modifie aussitôt, ce qu'attend un nouvel utilisateur.
- Seul un IBAN masqué (code pays, clé, 4 derniers caractères) est stocké.
- Pas de devise par compte : le multi-devises est hors périmètre.
- Un compte qui a des opérations ne se supprime pas : il s'archive (il
  disparaît de la saisie, son historique reste).

### Catégories
Deux niveaux, contrôlés par l'application ; une sous-catégorie prend le type
(dépense, revenu, virement) de son parent, et un changement de type du parent
est reporté sur ses enfants. Un jeu de catégories par défaut est créé par la
migration et reste entièrement modifiable. Le tri suit l'ordre alphabétique
français (accents ignorés), calculé en Go sur ces quelques dizaines de
lignes plutôt qu'avec une collation ICU.

### Virements entre comptes
Un virement crée deux opérations de signes opposés reliées par une ligne de
la table `transfers` ; il se modifie et se supprime comme un tout. Ses
jambes n'ont pas de catégorie et ne sont jamais modifiées par lot. Le
rapprochement des virements importés (jalon 4) réutilisera ce lien.

### Recherche et sélection
- Recherche plein texte FTS5 en index externe (le texte n'est pas dupliqué),
  synchronisée par déclencheurs, insensible à la casse et aux accents ;
  chaque mot saisi devient un préfixe obligatoire et les caractères
  spéciaux de FTS5 sont neutralisés.
- Un seul mécanisme de sélection, `ledger.Filter` : la liste l'affiche,
  l'édition par lot s'applique soit aux opérations cochées, soit à tous les
  résultats du même filtre (transmis tel quel par le formulaire).
- Pagination par décalage (50 lignes) : suffisante à l'échelle d'un foyer,
  voir `PERFORMANCE.md`.

### Saisie rapide
Montant (clavier décimal, focus automatique) puis libellé, Entrée valide :
la date du jour et le dernier compte utilisé sont préremplis, les 50 derniers
libellés sont proposés par la liste native du navigateur (`datalist`).
« Enregistrer et saisir une autre » conserve type, date et compte. Après
enregistrement, l'utilisateur revient à la page d'où il venait (liste
filtrée comprise) avec un message de confirmation.

### Interface : HTMX, pas d'Alpine.js
- HTMX 2.0.11 (licence 0BSD) est vendorisé dans `internal/web/static/` :
  recherche instantanée et pagination sans rechargement. Configuration :
  aucun historique en `localStorage` (il contiendrait des données
  bancaires), pas d'`eval`, requêtes vers l'application seule. Sans
  script, les mêmes formulaires fonctionnent normalement.
- Alpine.js n'est pas retenu : les deux comportements utiles (confirmation
  avant suppression, « tout cocher ») tiennent en vingt lignes de
  JavaScript sans dépendance (`app.js`). Les champs qui dépendent du type
  d'opération s'affichent par CSS (`:has()`), sans script.
- Les fichiers statiques sont compressés (gzip) une fois au démarrage.
- Les pastilles de couleur des catégories sont des cercles SVG (attribut
  `fill`) : la politique CSP interdit les styles en ligne.
- Les messages de confirmation (« Opération enregistrée. ») passent par un
  cookie d'une minute, sans aucune donnée bancaire.

### Schéma au fil des jalons
Les colonnes qui ne servent qu'aux jalons suivants (date de valeur, tiers,
empreinte d'import, provenance) seront ajoutées par leur migration, quand
elles serviront.

## Import (jalon 3)

### Profils provisoires faute d'exports réels
Aucun export réel de la Société Générale ni de BNP Paribas n'a été fourni :
les profils `sg.yaml` et `bnp.yaml` et les fichiers de `testdata/` sont
**synthétiques**, marqués comme tels, et devront être vérifiés sur des
exports réels anonymisés. L'import ne dépend pas de leur exactitude : un
fichier non reconnu passe par le choix manuel des colonnes, qui peut être
enregistré comme profil.

### Un seul mécanisme de lecture : `Mapping`
Un profil (YAML) liste, pour chaque donnée, les intitulés de colonne
possibles ; il est reconnu quand une des 30 premières lignes contient une
date, un libellé et un montant (ou débit et crédit). Il se résout alors en
`Mapping` (numéros de colonnes, ligne d'en-tête, format de date, sens des
montants). Le choix manuel des colonnes produit directement un `Mapping`.
Les profils fournis sont embarqués ; un fichier du même nom dans
`/data/profiles/` les remplace, et tout autre fichier `.yaml` y ajoute un
profil, essayé en premier. Les profils sont relus à chaque dépôt : une
modification s'applique sans redémarrage. Format YAML (`go.yaml.in/yaml/v3`)
plutôt que JSON : il se commente et se modifie à la main.

### Lecture des fichiers
- Format reconnu à la signature, pas à l'extension : certains « .xls »
  bancaires sont des CSV, d'autres des pages HTML (refusées avec un message
  invitant à réexporter).
- CSV lu en flux ; encodage UTF-8 (avec ou sans BOM) sinon Windows-1252
  (sur-ensemble d'ISO-8859-1) ; séparateur deviné parmi `;` `,` tabulation
  `|` par la régularité du nombre de colonnes.
- XLSX : `excelize`, lu en flux, valeurs brutes (dates en numéro de série,
  montants sans format). XLS : `extrame/xls` (dernière version, 2020), qui
  décode correctement les textes accentués contrairement à `xlsReader`
  (essayé) ; une panique de la bibliothèque devient un message d'erreur.
- Les montants à la française sont lus par `money.Parse` ; un nombre à
  virgule flottante issu d'un classeur est arrondi au centime.
- `golang.org/x/text` (déjà lié par excelize) fournit le décodage
  Windows-1252 et la suppression des accents ; il remplace la table
  d'accents écrite au jalon 2 (un seul mécanisme : `platform/text.Fold`).

### Déduplication par rang d'occurrence
Empreinte SHA-256 de (compte, date, montant, libellé normalisé, rang de
l'occurrence identique dans le fichier), unique par compte en base. Deux
achats identiques le même jour reçoivent les rangs 1 et 2 et sont tous deux
importés ; un export qui chevauche le précédent reproduit les mêmes rangs
pour les jours complets qu'il contient, et n'ajoute rien. Limite connue : un
export qui coupe une journée au milieu de deux opérations identiques peut
décaler les rangs de ce jour-là. Une opération importée puis supprimée à la
main sera réimportée par un fichier qui la contient.

### Brouillon en base, pas en mémoire
Le fichier déposé est copié dans `DATA_DIR/imports/` (le formulaire est lu
en flux, sans fichier temporaire : l'image `scratch` n'a pas de `/tmp`), puis
ses lignes interprétées sont rangées par lots dans `import_lines`. L'aperçu
est paginé (100 lignes), la sélection est une colonne de cette table et la
validation un seul `INSERT … SELECT` dans une transaction. Un brouillon
abandonné depuis 24 h est purgé au dépôt suivant (pas de tâche de fond).
Après ces opérations lourdes, `db.ReleaseMemory` rend au système le cache
SQLite et le tas Go : la RAM au repos repasse de 55 à 30 Mo après un import
de 10 000 lignes.

### Écriture des opérations importées
L'import insère directement dans `transactions` les lignes qu'il a
validées (date, libellé borné, montant non nul), en une seule requête :
passer par `ledger.CreateTransaction` imposerait une transaction et des
contrôles de compte et de catégorie par ligne, inutiles ici. Le grand livre reste seul à modifier une opération
existante. Une annulation supprime les opérations portant l'identifiant de
l'import.

### Catégorie suggérée à l'aperçu
Elle viendra des règles de catégorisation du jalon 4, appliquées à l'import :
un mécanisme provisoire (par exemple d'après l'historique) serait un second
mécanisme de catégorisation, à proscrire.

### Rapprochement
Le solde relevé sur le site de la banque est enregistré sur le compte
(`bank_balance`, `bank_balance_date`) et comparé au solde calculé à la même
date ; l'écart renvoie vers la liste des opérations non pointées jusqu'à
cette date. La vue de pointage rapide (jalon 9) réutilisera ce même solde.
C'est la troisième étape de l'assistant d'import, aussi accessible depuis
chaque compte.

## Règles et virements internes (jalon 4)

### Un seul mécanisme de catégorisation automatique
Le package `categorize` porte les règles ; il sert à la fois à l'import
(catégorie suggérée à la mise en attente, visible dans l'aperçu, enregistrée
à la validation) et à l'application rétroactive. Aucune autre forme de
suggestion (par exemple d'après l'historique) n'existe.

- « Contient » compare la forme normalisée du libellé et du motif (casse,
  accents et espaces ignorés, `text.Fold`) ; une expression régulière (RE2,
  sans risque d'emballement) s'applique au libellé tel quel, casse ignorée.
- Ordre d'évaluation : priorité décroissante, puis motif le plus long (le
  plus précis), puis le plus ancien ; la première règle qui s'applique
  l'emporte.
- L'application rétroactive ne touche par défaut que les opérations sans
  catégorie ; l'option « y compris celles déjà catégorisées » est explicite.
  Les jambes de virement ne sont jamais catégorisées.
- Elle se fait en Go (lecture en flux des libellés, mise à jour groupée par
  catégorie) : SQLite ne connaît pas `text.Fold`, et stocker un libellé
  normalisé n'aurait servi qu'à cela.
- Règle « en un clic » : la page d'une opération propose une règle
  préremplie (deux premiers mots significatifs du libellé, sans références,
  dates ni mots ajoutés par la banque) et la catégorie de l'opération.
  L'édition par lot peut aussi créer la règle « le libellé contient le
  texte recherché ».
- Les règles vivent sous `/regles` (et non `/categories/regles`, que le
  routeur confondrait avec `/categories/{id}`).

### Virements internes
- Candidat : une sortie et une entrée de même montant sur deux comptes
  différents, à trois jours d'écart au plus, aucune n'étant déjà reliée.
  Les paires sont proposées, jamais reliées d'office : deux montants égaux
  peuvent être une coïncidence.
- Relier une paire réutilise le lien des virements saisis (table
  `transfers`) : les deux opérations perdent leur catégorie et ne comptent
  plus ni en dépense ni en revenu.
- « Ce n'est pas un virement » écarte la paire durablement
  (`transfer_dismissals`) ; défaire un virement relié l'écarte aussi.
- L'annulation d'un import défait d'abord les virements auxquels ses
  opérations participent (`ledger.UnlinkTransfers`, même mécanisme), pour ne
  jamais laisser un virement à une seule jambe.
- La liste des opérations et la fin d'un import signalent les paires à
  vérifier ; le décompte ne charge pas les opérations.

## Récurrences (jalon 5)

### Périodicité : un nombre et une unité
`calendar.Period` (tous les N jours, semaines ou mois) couvre hebdomadaire,
mensuelle, bimestrielle, trimestrielle, semestrielle, annuelle et
personnalisée. Chaque échéance se calcule depuis la date de départ, sans
dérive : une série du 31 tombe le 28 ou le 29 février puis de nouveau le 31
mars ; l'option « dernier jour du mois » couvre les salaires de fin de mois.
Chaque récurrence précise en outre le report d'une échéance tombant un jour
non ouvré (voir le jalon 6).

### Détection
- Groupe : compte, sens (entrée ou sortie) et libellé normalisé sans les
  mots contenant un chiffre (références, dates d'échéance). C'est aussi la
  **signature** d'une série.
- Série : montants à ±10 % d'une même valeur centrale (rapport max/min
  ≤ 1,1/0,9) ; une hausse de prix plus forte ouvre une nouvelle série.
- Périodicité : chaque date est comparée à l'échéance attendue d'une série
  ancrée sur la première occurrence, recalée de l'écart médian ; conforme à
  ±3 jours. La régularité retient la plus faible des deux parts : dates
  conformes parmi les occurrences, et parmi les échéances attendues (une
  échéance manquée compte). Seules les périodicités proches de l'écart
  médian sont évaluées.
- Retenue si : trois occurrences au moins, régularité ≥ 60 %, pas deux
  échéances manquées de suite, score ≥ 40.
- Score (0 à 100) = régularité × confiance due au nombre d'occurrences (50 %
  à 3, 100 % à partir de 6) × stabilité du montant (1 si fixe, sinon
  1 − coefficient de variation, 0,7 au moins).
- Montant proposé : fixe s'il ne varie jamais, sinon moyenne des trois
  dernières occurrences, marquée « estimée ».
- La récurrence validée part de la dernière occurrence constatée ; une
  échéance en retard de moins de trois jours (délai bancaire) reste attendue.
- Historique examiné : trois ans, hors virements internes. La détection se
  fait à l'affichage (49 ms pour 20 000 opérations), rien n'est stocké.

### Validation
Une série validée, modifiée puis enregistrée, ou saisie à la main devient
une récurrence ; sa signature, fixée à la création (celle de la série
d'origine, sinon tirée du libellé), empêche que la série soit de nouveau
proposée, même si le libellé est modifié. Une série rejetée est mémorisée
(`recurring_dismissals`). Une récurrence peut être suspendue sans être
supprimée ; seules les récurrences actives entreront dans le prévisionnel.

### Navigation à cinq onglets
La barre d'onglets mobile compte cinq entrées (Accueil, Opérations,
Prévisionnel, Importer, Plus) ; « Plus » mène à une page qui liste les
écrans secondaires. Le prévisionnel a pris au jalon 6 la place des
récurrences, consultées moins souvent et accessibles depuis son écran. La
tablette (768 à 1023 px) garde ces cinq entrées en haut de page. À partir
de 1024 px s'y ajoutent Budgets, Épargne et Rapports (sans icônes jusqu'à
1279 px) ; « Plus » reste et mène aux autres écrans (récurrences,
abonnements, comptes, catégories, règles, étiquettes, alertes, virements,
historique des imports), dont le nombre a crû au jalon 9. Les
alertes sont signalées par une cloche dans l'en-tête, à toutes les largeurs ;
sous 400 px, le nom de l'application laisse la place aux actions de
l'en-tête (le logo reste). Le tout en CSS, sans script.

### Construction de l'interface
`web.New` reçoit les services du domaine regroupés dans `web.Services`
plutôt qu'en paramètres successifs.

## Prévisionnel (jalon 6)

### Un moteur pur, un service qui le nourrit
`forecast.Compute` est une fonction pure et déterministe : solde de départ,
mouvements datés, estimation mensuelle des dépenses variables et seuil en
entrée ; solde jour par jour, lignes avec solde cumulé, point bas, périodes
sous le seuil et totaux en sortie. Le service rassemble les données (soldes
et non pointées par `ledger`, récurrences par `recurring`) puis l'appelle.
Les scénarios « et si » (jalon 10) ajouteront ou retireront des mouvements
avant ce même appel : aucun second moteur.

### Ce qui entre dans le calcul
- **Solde de départ** : solde au soir d'aujourd'hui de tous les comptes
  courants actifs (ou du compte choisi). Au choix, le solde **pointé** : les
  opérations non pointées en sont retirées et comptées le lendemain, une
  ligne par compte, pour que le solde prévu reste juste.
- **Échéances des récurrences actives** après aujourd'hui, à leur date
  effective. Une échéance du jour est supposée déjà passée.
- **Opérations planifiées** (datées après aujourd'hui), sauf les virements
  entre deux comptes couverts, sans effet sur leur total.
- **Dépenses variables** : moyenne mensuelle, par catégorie, des dépenses
  des 3, 6 ou 12 derniers mois complets (3 par défaut, désactivable), hors
  virements internes et hors opérations d'une série couverte par une
  récurrence (même signature). Les mois antérieurs à la date du solde
  initial du compte le plus ancien ne comptent pas. L'estimation est répartie
  jour par jour au prorata de la longueur de chaque mois, en centimes cumulés
  arrondis (aucune dérive d'arrondi).
- Le même jour, les revenus passent avant les dépenses.
- Les totaux expliquent l'écart : solde de départ + revenus + dépenses
  récurrentes + dépenses planifiées + dépenses variables = solde prévu.

### Jours non ouvrés
Samedis, dimanches et les onze jours fériés légaux (Pâques calculé, sans
dépendance ; les jours propres à l'Alsace-Moselle ne sont pas comptés).
Chaque récurrence choisit le report de ses échéances : jour ouvré suivant
(par défaut, comme la plupart des prélèvements), précédent (salaires) ou
aucun. Une échéance garde sa date théorique, qui l'identifie, et sa date
effective, qui la place dans le calcul.

### Exclusion ponctuelle
Une échéance écartée reste affichée, barrée, sans peser sur le solde ; elle
est repérée par la récurrence et sa date théorique (`forecast_exclusions`),
et le reste même si la règle de report change. Les exclusions passées sont
purgées à l'écriture suivante. Les simulations plus larges passeront par les
scénarios.

### Réglages communs au foyer
Seuil d'alerte (0 € par défaut, négatif pour un découvert autorisé), solde
de départ (toutes les opérations ou pointées seulement) et profondeur de la
moyenne sont enregistrés dans `forecast_settings` (une ligne) : les alertes
du jalon 8 reprendront ce seuil.

### Horizon et date cible
24 mois au plus. Par défaut, trois mois ; des raccourcis proposent la fin du
mois, 3 et 6 mois, 1 et 2 ans. La requête est un GET (`compte`, `date`) :
l'adresse d'un prévisionnel se garde en favori.

### Courbe en SVG rendu côté serveur
Le tracé en escalier est calculé en Go dans un repère de 1000 × 1000 étiré à
la largeur de l'écran (`preserveAspectRatio="none"`, traits non déformés par
`vector-effect`) ; textes et point bas sont placés en pourcentages, donc
jamais déformés. Couleurs par classes CSS : les deux thèmes s'appliquent sans
code. Aucun script, compatible avec la CSP ; la liste des échéances sert
d'alternative textuelle. uPlot (≈ 50 Ko) n'apportait que l'interactivité au
survol, superflue ici.

## Épargne (jalon 7)

### Réglages à part, un compte reste un compte
Un compte d'épargne est un compte ordinaire (type « Épargne ») : ses
opérations, virements, imports et rapprochements passent par le grand
livre. Ses réglages propres vivent dans `savings_settings` (1-1) : produit,
taux, méthode d'intérêts, plafond, blocage, pénalité. Ils sont saisis dans
le formulaire du compte (section affichée en CSS pour le type « Épargne »),
vérifiés avant toute écriture, et supprimés si le compte change de type.

### Modèles préremplis, sans valeur officielle
Livret A, LDDS, LEP, PEL, livret bancaire, assurance-vie, compte à terme,
autre. Les valeurs ont été relevées le 31/10/2026 et sont affichées comme
des valeurs par défaut à vérifier, avec cette date :
- plafonds : Livret A 22 950 €, LDDS 12 000 €, LEP 10 000 €, PEL 61 200 € ;
- taux en vigueur depuis le 01/08/2026 : Livret A et LDDS 1,70 %, LEP
  2,50 % ; PEL ouvert depuis le 01/01/2026 : 2 % (fixé à l'ouverture). Ces
  taux viennent de sources tierces concordantes, faute d'accès à la source
  officielle depuis l'environnement de développement ;
- livret bancaire, assurance-vie, compte à terme : taux laissé vide (propre
  à chaque contrat).
Méthode : quinzaine pour les livrets, composée annuelle pour le PEL et
l'assurance-vie, simple pour le compte à terme.

### Intérêts
Taux en centièmes de point (170 = 1,70 %), un seul taux par compte : un
changement de taux en cours d'année s'applique à toute l'année calculée.
- **Quinzaine** (livrets réglementés) : chaque quinzaine rapporte taux/24
  sur le solde qui a valeur à son début ; un versement compte à partir de
  la quinzaine suivante, un retrait dès le début de la quinzaine où il a
  lieu ; capitalisation au 31/12.
- **Simple / composé annuel** : au jour le jour (taux / 365 ou 366, valeur
  J+1), crédités au 31/12 ; en intérêts simples, les intérêts déjà crédités
  ne rapportent rien.
- Le solde initial d'un compte rapporte dès sa date (il existait au début du
  jour).
- **Inscription** : une opération « Intérêts AAAA » datée du 31/12, marquée
  par `interest_year` (une par compte et par année, index unique),
  modifiable comme toute opération si la banque a versé un autre montant ;
  déplacée vers un autre compte, elle redevient ordinaire.
  - automatique, au passage à la nouvelle année, pour les comptes qui le
    demandent, à partir de l'année où leurs réglages ont été enregistrés :
    jamais d'écritures rétroactives inattendues ;
  - à la demande, depuis la vue Épargne, pour les années closes restantes.
  Un compte dont on importe les relevés doit désactiver l'inscription
  automatique : les intérêts figurent déjà dans le relevé.

### Disponible et bloqué
Deux formes de blocage, exclusives : tout le solde jusqu'à une date
(PEL, compte à terme) ou chaque versement pendant N mois (déblocage
glissant, comme l'épargne salariale), les retraits puisant d'abord dans les
fonds libres. Le calcul est fait à une date donnée, d'après tous les
mouvements. Une dépense ou un virement qui dépasse le disponible déclenche
un avertissement avec la pénalité estimée (pourcentage du montant
anticipé) ; l'opération n'est enregistrée qu'une fois le retrait confirmé.
L'avertissement porte sur les nouvelles saisies ; l'import enregistre ce
que la banque a déjà exécuté.

### Versements programmés : des récurrences
Une récurrence peut être un virement vers un autre compte du foyer
(`to_account_id`) : c'est le versement programmé, sans mécanisme propre. Le
prévisionnel en compte le débit et le crédit selon les comptes couverts
(rien si les deux le sont : ils s'annulent).

### Projection et intérêts estimés
Le prévisionnel couvre au choix les comptes courants, toute l'épargne ou un
compte. Pour un compte rémunéré, il ajoute au 31/12 de chaque année de
l'horizon les intérêts estimés d'après les mouvements passés et prévus (les
intérêts projetés rapportent l'année suivante), sauf si ceux de l'année sont
déjà inscrits. La vue Épargne affiche la projection à une date choisie par
ce même moteur et renvoie à son détail jour par jour.

### Tâche quotidienne
Prévue au jalon 8, elle est arrivée avec l'inscription des intérêts : un
seul `time.Timer`, réarmé chaque jour à 00h05 (heure locale), exécute les
travaux au démarrage puis quotidiennement (intérêts, puis alertes au
jalon 8). Un travail en échec est
journalisé et retenté le lendemain. À l'arrêt, le serveur attend la fin du
travail en cours avant de fermer la base.

## Budgets et alertes (jalon 8)

### Enveloppes
- Un plafond mensuel par catégorie de dépenses ; une catégorie parente
  couvre ses sous-catégories, sauf celles qui ont leur propre enveloppe :
  une dépense compte dans l'enveloppe la plus précise, jamais dans deux.
- Consommé : dépenses nettes du mois (remboursements déduits), hors
  virements internes, tous comptes confondus.
- Report facultatif, par enveloppe : le reste ou le dépassement de chaque
  mois s'ajoute au suivant, à partir du mois de création.
- État en toutes lettres (« Dans le budget », « Bientôt atteint »,
  « Dépassé »), la couleur et la jauge ne font que souligner. Le seuil
  « bientôt atteint » est celui de l'alerte d'enveloppe (80 % par défaut) :
  un seul réglage.
- Tendance : dépenses des catégories budgétées sur douze mois face au total
  actuel des enveloppes, en barres SVG avec tableau équivalent.
- Dans le prévisionnel des comptes courants, le plafond d'une enveloppe
  remplace la moyenne des dépenses de ses catégories (marqué « Enveloppe »).
  Pour un compte seul ou l'épargne, la moyenne reste la règle : les
  enveloppes valent pour le foyer.

### Reste à vivre
Calculé par le prévisionnel (pas de second calcul) sur les comptes courants
pour le mois en cours : entrées reçues et attendues − charges et dépenses
prévues à venir − versements vers l'épargne et les autres comptes (faits et
à venir) − dépenses déjà faites. Les virements entre comptes courants ne
comptent pas. Sans estimation des dépenses variables : c'est ce qui leur
reste.

### Alertes : un seul système
- Sept types, chacun activable avec un paramètre : enveloppe presque
  épuisée (% consommé) ou dépassée ; solde prévisionnel sous le seuil
  (horizon en jours, seuil du prévisionnel) ; dépense inhabituelle (plus de
  N fois la médiane des dépenses du même libellé sur un an, à défaut de la
  même catégorie ; dépenses de 20 € et plus des sept derniers jours) ;
  abonnement dont le montant (écart toléré en %, sauf montant estimé) ou la
  date (plus de 3 jours) change, d'après le dernier prélèvement de la même
  série ; échéance proche ; pas d'import depuis N jours ; déblocage de
  fonds d'épargne imminent.
- Une alerte par situation, repérée par une clé (enveloppe et mois, première
  date sous le seuil, opération…) : acquittée, elle ne revient pas. Les
  alertes acquittées sont purgées après 90 jours.
- Examen après chaque modification réussie (toute requête POST authentifiée
  aboutissant à une redirection : saisie, import, budget, récurrence…),
  en arrière-plan et regroupé, et chaque nuit. Jamais d'interrogation
  périodique ; un examen complet prend 44 ms sur 20 000 opérations.
- Réglages communs au foyer : BuddyGet n'a pour l'instant qu'un compte
  utilisateur ; ils deviendront personnels avec la gestion de plusieurs
  utilisateurs.

### Notifications sortantes
E-mail (SMTP avec STARTTLS obligatoire ou TLS dès la connexion,
authentification facultative) et webhook (POST en texte brut, en-tête
`Title` reconnu par ntfy), désactivés par défaut : ce sont les seules
connexions sortantes de l'application. Rythme : immédiat, récapitulatif
quotidien ou hebdomadaire (le lundi). Les notifications sont discrètes par
défaut (nombre et types d'alerte, ni libellé ni montant). Sans canal actif,
les alertes sont marquées notifiées : activer un canal n'envoie pas
l'historique. Un envoi en échec est retenté au passage suivant. Le mot de
passe SMTP est stocké dans la base (sur le NAS), jamais réaffiché ni
journalisé.

## Saisie, pointage, abonnements et rapports (jalon 9)

### Étiquettes
Libres, en complément des catégories, dans le grand livre : saisies
séparées par des virgules (doublons écartés, casse et accents ignorés),
filtre de la liste, ajout et retrait par lot sur une sélection ou une
recherche (mécanisme d'édition par lot unique), rapport par étiquette.
Renommage et retrait global depuis la page Étiquettes ; une étiquette qui
ne sert plus disparaît. Comme pour la catégorie, les virements entre
comptes n'en portent pas.

### Saisie ultra-rapide
- Modèles de saisie : créés depuis une opération existante (« Enregistrer
  comme modèle »), proposés en tête de la saisie ; un clic crée l'opération
  du jour.
- Dupliquer une opération, ou la dernière saisie (ni importée, ni écriture
  d'intérêts) : le formulaire est prérempli, daté du jour.
- Dictée : l'API de reconnaissance vocale du navigateur, seulement si elle
  existe (le bouton reste caché sinon), en demandant le traitement sur
  l'appareil quand le navigateur le propose. BuddyGet n'envoie rien et
  n'impose aucun service ; le navigateur reste maître de son moteur. « Courses
  23 euros 50 » remplit le montant (23,50) et le libellé (Courses).

### Pointage : un seul mécanisme de vérification
La page de rapprochement d'un compte devient la vue de pointage : solde de
la banque à une date, solde pointé, reste à pointer, et la liste des
opérations non pointées du solde initial à cette date. Pointer une
opération (case, ou balayage vers la droite sur mobile) l'envoie par HTMX ;
la réponse remplace la ligne et, hors de la ligne, les soldes. Sans script,
chaque case est un bouton de formulaire classique. Quand le reste à pointer
tombe à zéro, les comptes concordent avec le relevé.

### Pièces jointes
- Photo (JPEG, PNG, WebP, GIF) ou PDF ; type reconnu au contenu, non à
  l'extension. Taille limitée par `MAX_UPLOAD_MB` (le même réglage que
  l'import), place totale par `ATTACHMENTS_QUOTA_MB` (1 Go par défaut).
- Images redressées selon leur orientation EXIF, réduites à 1 600 px de
  large et recompressées en JPEG (qualité 75). Les noyaux d'interpolation
  de `x/image/draw` allouent un tampon de flottants de la largeur cible par
  la hauteur source (180 Mo pour une photo de 12 Mpx) : la réduction se
  fait donc par moyenne de blocs (facteur entier, lecture YCbCr sans
  allocation), puis interpolation bilinéaire sans tampon, 38 Mo en tout.
  Une seule image est réduite à la fois ; au-delà de 40 Mpx, refus.
- Fichiers dans `DATA_DIR/attachments`, sous un nom aléatoire, écrits par
  fichier temporaire puis renommage. Les PDF se téléchargent (la visionneuse
  du navigateur ne s'accommode pas de la politique de sécurité), les images
  s'affichent. Les fichiers d'opérations supprimées (ou d'imports annulés)
  sont effacés par la tâche quotidienne.

### Abonnements
Une case « C'est un abonnement » sur une récurrence ; la vue Abonnements
filtre les récurrences actives ainsi marquées (pas de seconde détection) :
coût mensuel équivalent et annuel, prochain prélèvement, et deux indices à
vérifier soi-même :
- doublon possible : même commerçant (un mot significatif commun dans le
  libellé, hors mentions bancaires) ou même catégorie et montant à 10 % ;
- peut-être oublié : abonnement catégorisé, ouvert depuis plus de 90 jours,
  sans aucune autre dépense de sa catégorie sur cette période (une salle de
  sport sans aucune autre dépense de sport, par exemple).

### Rapports
Mensuel et annuel, comparés à la période précédente. La catégorie décide :
dépense, revenu, ou ni l'un ni l'autre (catégorie « virement », comme
l'épargne) ; sans catégorie, le signe. Les remboursements viennent en
déduction de leur catégorie ; les virements internes sont exclus. Taux
d'épargne = (revenus − dépenses) / revenus. Principaux commerçants :
libellés regroupés sans les mots à chiffres (dates, références) ni les
mentions bancaires (CB, PRLV…). L'annuel ajoute les mois, dépenses face aux
revenus mensuels moyens. La comparaison budget / réel reste la tendance de
l'écran Budgets (lien depuis le rapport). Les rapports s'exportent en PDF ou en CSV (jalon 11).

## Crédits, projets, lissage et scénarios (jalon 10)

### Crédits
- Crédit amortissable à mensualités constantes : capital, taux nominal
  annuel (millièmes de point), durée (600 mois au plus), assurance mensuelle
  fixe, première échéance. Mensualité = P·r / (1 − (1 + r)^−n), arrondie au
  centime ; à taux nul, P / n arrondi au centime supérieur. Les intérêts de
  chaque échéance sont arrondis au centime ; la dernière échéance solde le
  capital restant (l'écart d'arrondi s'y concentre).
- Un remboursement anticipé réduit le capital avant l'échéance qui le suit :
  la mensualité reste, la durée raccourcit (le cas courant en France). S'il
  solde le crédit, la ligne de clôture prend la date du remboursement et la
  mensualité s'arrête ce jour-là.
- La mensualité (assurance comprise) est une récurrence générée et tenue à
  jour par le crédit : création, modification, remboursement anticipé et
  suppression la mettent à jour, sans double saisie. Supprimée à la main,
  elle est recréée à la modification suivante ; un crédit soldé avant sa
  première échéance n'en a plus.

### Projets
- Un projet (montant, date, compte d'épargne de rattachement) suit une
  cagnotte virtuelle : la part du compte affectée à la main, sans compte
  bancaire supplémentaire. Les cagnottes d'un compte ne dépassent jamais son
  solde ; un compte peut porter plusieurs projets.
- Versement suggéré = reste à mettre de côté / mois restants (au moins un),
  arrondi au centime supérieur. Date d'atteinte au rythme du versement
  programmé (ramené au mois) ; sans versement, le projet est signalé en
  retard.
- « Créer le versement » ajoute une récurrence mensuelle (versement
  programmé du jalon 7) du compte choisi vers le compte du projet, du mois
  suivant à la date cible ; elle entre ainsi au prévisionnel. Les dépenses
  du projet sont les opérations portant son étiquette (une seule).

### Lissage
- Option d'une récurrence de dépense espacée d'au moins deux mois : son
  montant est provisionné en N parts mensuelles (N = mois entre deux
  échéances), du jour de l'échéance précédente au mois qui précède la
  suivante. Les parts sont calculées par différences de cumuls arrondis :
  un cycle totalise exactement l'échéance.
- La provision est mise de côté sur un compte d'épargne (virements prévus
  vers ce compte, puis depuis lui à l'échéance) ou sans compte (simple mise
  de côté dans le solde). À l'échéance, la « provision utilisée » compense
  le paiement.
- Prévisionnel : seules les parts datées de l'horizon sont mises de côté et
  utilisées ; celles d'avant aujourd'hui, inconnues du solde de départ, ne
  compensent rien (le calcul ne surestime jamais l'argent disponible). Les
  provisions ont leur ligne dans le détail du calcul.
- Reste à vivre : régime établi, chaque mois porte sa part et l'échéance du
  mois est entièrement couverte ; les virements passés vers ou depuis le
  compte de lissage sont déjà dans les opérations du mois.
- Une échéance écartée n'est ni provisionnée ni compensée.

### Scénarios « et si »
- Un scénario est un calque nommé appliqué par le moteur unique du
  prévisionnel ; il ne modifie aucune donnée. Six modifications :
  opération ponctuelle, opération récurrente (tous les N mois), arrêt d'une
  récurrence à une date, hausse ou baisse d'une récurrence (en euros ou en
  %, sur la valeur absolue : une hausse augmente la dépense ou le revenu),
  nouveau crédit, remboursement anticipé d'un crédit suivi.
- Le moteur projette des « plans » : une récurrence et ses changements de
  montant datés (un montant nul l'arrête, définitivement). Échéances,
  lissage et intérêts estimés en découlent sans mécanisme parallèle. Une
  variation prend effet à la première échéance au jour habituel de la série
  (une série du 31 reprend un 31).
- Nouveau crédit : ses mensualités seulement ; le capital finance
  directement l'achat (un apport se saisit en opération ponctuelle).
  Remboursement anticipé : le versement, borné au capital restant dû, puis
  la suppression des mensualités devenues inutiles.
- Comparaison avec la référence : solde à la date cible, point bas, écart
  cumulé (différence des soldes à la date cible, effet de toutes les
  modifications) ; la courbe de référence est tracée en pointillés.
- Conversion en données réelles, dans l'ordre des dates : opération
  planifiée, récurrence créée, récurrence arrêtée (fin la veille, ou
  suspendue si elle n'a pas commencé), récurrence scindée (l'ancienne
  s'arrête, une nouvelle reprend au nouveau montant), crédit créé,
  remboursement anticipé enregistré ; puis le scénario est supprimé.

## Partage du foyer, exports et sauvegardes (jalon 11)

### Partage du foyer
- Les membres du foyer sont distincts des comptes de connexion ; leur revenu
  mensuel sert à la répartition au prorata.
- Une opération partagée garde son payeur et la part de chacun, en
  centimes : parts égales, au prorata des revenus (méthode du plus fort
  reste, produits calculés sur 128 bits) ou montants libres, dont la somme
  doit égaler l'opération. Le partage est contrôlé avant l'enregistrement de
  l'opération : une saisie refusée n'enregistre rien.
- Solde d'un membre = avancé − sa part + remboursements faits − reçus ; une
  recette partagée (remboursement d'assurance…) compte en sens inverse. Les
  remboursements à faire apparient les plus gros débiteurs aux plus gros
  créanciers.
- Un remboursement entre deux comptes suivis crée le virement interne
  correspondant (section 4) ; sans comptes, il est simplement noté (espèces).
  L'annuler supprime aussi son virement. Les virements ne se partagent pas.
- Un membre présent dans un partage ne se supprime pas : il s'archive.

### Exports
- Point d'entrée unique « Exports », plus des raccourcis : la liste des
  opérations exporte sa recherche, les rapports leur période.
- CSV pour un tableur français : UTF-8 avec BOM, point-virgule, virgule
  décimale ; les cellules de texte qui commencent par `= + - @` sont
  préfixées d'une apostrophe (injection de formules).
- Les opérations sont lues au fil de l'écriture (`ledger.Each`) ; le dossier
  ZIP écrit le CSV, la synthèse PDF puis copie les pièces jointes une à une,
  sans recompression (JPEG et PDF le sont déjà).
- PDF : `go-pdf/fpdf`, avec les polices standard du format (texte converti
  en Windows-1252, qui couvre le français et l'euro) : aucune police
  embarquée, aucun navigateur. Les rapports et la synthèse restent courts ;
  le détail des opérations est dans le CSV.

### Sauvegardes
- Instantané cohérent par `VACUUM INTO`, sans arrêter BuddyGet, puis une
  archive tar compressée (gzip) avec la base, les pièces jointes et un
  manifeste ; écrite sous un nom temporaire, renommée une fois complète.
- Chiffrement au format age (phrase de passe, scrypt) : standard, documenté,
  déchiffrable sans BuddyGet (`age -d`). Le facteur de travail de scrypt est
  ramené à 2^15 (32 Mo de mémoire au lieu de 256 par défaut) pour tenir
  dans les 128 Mo du conteneur ; la longueur minimale de la phrase de passe
  (12 caractères) compense. La phrase de passe vient de l'environnement
  (`BACKUP_PASSPHRASE`) : elle n'est pas dans la base, donc pas dans les
  sauvegardes.
- Sauvegarde automatique avec la tâche quotidienne ; rotation des seules
  sauvegardes automatiques (celles faites à la main sont gardées).
- Copie vers un second emplacement, désactivée par défaut : dossier monté
  (même rotation) ou WebDAV (`PUT` authentifié, rotation laissée au
  serveur). SFTP n'est pas proposé : WebDAV couvre Nextcloud et les NAS sans
  dépendance supplémentaire, le dossier monté couvre les partages réseau et
  les disques externes. Un échec est consigné et affiché sur l'écran.
- Restauration en deux temps : l'archive est déchiffrée, extraite (noms
  attendus seulement, taille bornée) et vérifiée (`integrity_check`,
  version du schéma au plus celle du programme) pendant que BuddyGet
  tourne ; elle est appliquée au démarrage suivant, avant l'ouverture de la
  base. BuddyGet s'arrête proprement et la politique `unless-stopped` le
  relance. Les données remplacées sont mises de côté dans
  `avant-restauration`.

