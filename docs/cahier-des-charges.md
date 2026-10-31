# Prompt Claude Code : BuddyGet, gestion du budget du foyer

## 0. Mode de travail

Tu es mon développeur principal. Avant d'écrire du code :
1. Lis ce cahier des charges en entier.
2. Propose un plan d'implémentation par jalons (section 11) et la structure du dépôt.
3. Liste les questions bloquantes éventuelles (maximum 5). Pour tout le reste, décide toi-même, note le choix dans `docs/DECISIONS.md` et avance.

Ensuite, développe jalon par jalon. À la fin de chaque jalon : tests au vert, `README` à jour, commit propre (Conventional Commits).

---

## 1. Contexte et objectif

**BuddyGet** est un outil **self-hosted** de gestion du budget d'un foyer (1 à 4 utilisateurs de confiance). Il tourne sur un **NAS Synology très peu puissant** via **Docker / Container Manager**.

Il doit permettre de :
1. **Importer** les exports `.xls` / `.xlsx` / `.csv` des applications **Société Générale** et **BNP Paribas** pour alimenter la base d'opérations.
2. **Saisir manuellement** des opérations.
3. **Calculer automatiquement un budget prévisionnel** à une date choisie, à partir du solde actuel et des dépenses/revenus récurrents.
4. **Gérer l'épargne** simplement : création de comptes d'épargne dans l'interface, intérêts (facultatifs), durée de blocage des fonds.

Langue de l'interface, des libellés et de la documentation : **français**. Devise : **EUR**. Fuseau : Europe/Paris. Format de dates : JJ/MM/AAAA.

---

## 2. Contraintes techniques (priorité n°1)

Ce sont des contraintes dures, à vérifier par des mesures et pas seulement affirmées.

| Contrainte | Cible |
|---|---|
| RAM au repos | **< 64 Mo** (objectif 30 Mo) |
| RAM pendant un import de 5 000 lignes | **< 150 Mo** |
| CPU au repos | ~0 % (aucun polling, aucune tâche de fond permanente lourde) |
| Image Docker | **< 50 Mo**, un seul conteneur, un seul processus |
| Démarrage | < 3 s |
| Architectures | `linux/amd64` **et** `linux/arm64` (NAS Celeron/Atom ou ARM) |
| Base de données | **SQLite** (fichier unique, mode WAL), pas de serveur de base externe |
| Pages | Rendu serveur, charge utile < 150 Ko par page hors graphiques |

**Stack imposée (sauf justification forte dans `DECISIONS.md`) :**
- **Backend : Go** (≥ 1.22), binaire statique unique, `CGO_ENABLED=0`.
- **SQLite sans CGO** : `modernc.org/sqlite`. Migrations SQL versionnées (`goose` ou équivalent minimal embarqué).
- **Frontend : rendu serveur** avec `html/template`, **HTMX** et un peu d'**Alpine.js**. Pas de framework SPA, pas de Node en production. Tous les assets (HTMX, Alpine, lib de graphiques légère type uPlot ou Chart.js) sont **embarqués localement** (`go:embed`) : aucune requête vers un CDN.
- **Lecture XLS/XLSX** : bibliothèque Go pure (`excelize` pour xlsx ; pour l'ancien `.xls` binaire, une lib pure Go type `extrame/xls`, ou à défaut message clair invitant à réexporter en CSV/XLSX). Les CSV sont lus en streaming.
- **Docker** : build multi-étapes, image finale `scratch` ou `gcr.io/distroless/static`, utilisateur non-root, `HEALTHCHECK`, volume unique `/data`, port configurable par variable d'environnement. Fournis un `docker-compose.yml` adapté à Synology (limites `mem_limit: 128m`, `cpus: 0.5`) et un `Dockerfile` buildx multi-arch.
- Limite les allocations : requêtes SQL paginées, pas de chargement complet des tables en mémoire, imports traités en flux et par transactions par lots.
- Index SQLite adaptés (date, compte, catégorie, hash d'import).

---

## 3. Modèle de données (proposition à affiner)

Montants stockés en **centimes (entiers)**, jamais en flottants.

- `accounts` : id, nom, type (`courant`, `epargne`, `especes`, `autre`), banque (`SG`, `BNP`, `autre`), IBAN masqué (optionnel), solde initial, date du solde initial, devise, archivé.
- `savings_settings` (1-1 avec un compte d'épargne) : taux annuel (optionnel), méthode d'intérêts (`aucun`, `simple`, `compose_annuel`, `quinzaine` pour type Livret A), fréquence de versement des intérêts, plafond (optionnel), **durée de blocage** (en mois) ou **date de déblocage**, pénalité de retrait anticipé (optionnelle). (Les objectifs d'épargne sont dans la table `goals`, pas ici.)
- `transactions` : id, compte, date d'opération, date de valeur (optionnelle), libellé brut, libellé normalisé, montant (signé), catégorie, tiers/commerçant, note, source (`import:<id>`, `manuel`, `recurrence`, `interets`), `import_hash` (unique par compte, pour la déduplication), pointé (booléen), lien vers un virement interne (`transfer_id`).
- `categories` : arborescence à 2 niveaux, couleur, type (dépense/revenu/virement).
- `category_rules` : motif (contient / regex), catégorie cible, priorité, appliquée automatiquement à l'import.
- `recurring_rules` : libellé, compte, montant (fixe ou estimé), périodicité (hebdo, mensuelle, bimestrielle, trimestrielle, semestrielle, annuelle, personnalisée), jour du mois ou règle (ex. « dernier jour ouvré »), date de début, date de fin (optionnelle), catégorie, `origine` (`manuelle` ou `detectee`), `actif`.
- `imports` : id, fichier, banque, profil de parsing, date, nb lignes lues / ajoutées / ignorées (doublons) / en erreur, statut annulable.
- `users` et `sessions` (voir section 8).
- `settings` : paire clé/valeur.
- *(ajouts section 7 bis)* `budgets` (enveloppes par catégorie et période, report), `alerts` et `alert_settings`, `scenarios` et `scenario_items`, `goals` (projets/objectifs, rattachés à un compte d'épargne), `loans` (crédits), `tags` et `transaction_tags`, `attachments`, `household_members` et `transaction_splits` (répartition entre membres), `quick_templates` (saisies fréquentes), `notification_channels`, `assets` (patrimoine, phase 2).

---

## 4. Fonctionnalité A : Import des fichiers bancaires

**Important** : ne devine pas les formats d'export des banques. Conçois l'import autour de **profils de parsing déclaratifs** et d'une assistance au mapping. Je fournirai des fichiers d'exemple anonymisés dans `testdata/` (SG et BNP). Si un fichier d'exemple manque, génère des fixtures plausibles, signale-le clairement, et rends le profil facile à ajuster.

Exigences :
1. **Formats** : CSV (séparateur `;` ou `,` auto-détecté, encodages UTF-8 / ISO-8859-1 / Windows-1252 auto-détectés, BOM géré), XLS, XLSX.
2. **Profils par banque** (SG, BNP Paribas, plus un profil « générique ») définis dans des fichiers de configuration (YAML ou JSON embarqués, surchargeables dans `/data/profiles/`) : lignes d'en-tête à ignorer, colonnes date / libellé / débit / crédit / montant, format de date, séparateur décimal, signe des montants.
3. **Détection automatique du profil** d'après les en-têtes. Si elle échoue : écran de **mapping manuel des colonnes**, avec possibilité d'enregistrer le résultat comme nouveau profil.
4. **Assistant d'import en 3 étapes** : (1) choix du fichier et du compte cible ; (2) **prévisualisation** avec les lignes nouvelles, doublons et erreurs, catégorie suggérée, possibilité de décocher des lignes ; (3) confirmation.
5. **Déduplication fiable** : hash calculé à partir de (compte, date, montant, libellé normalisé, rang de l'occurrence identique dans le fichier), de sorte que deux achats identiques le même jour ne soient pas fusionnés par erreur et qu'un réimport de fichiers qui se chevauchent n'ajoute rien de double.
6. **Catégorisation automatique** via `category_rules`, avec création de règle en un clic depuis une opération (« toujours catégoriser les libellés contenant X en Y »).
7. **Détection des virements internes** entre comptes du foyer (même montant opposé, dates proches) avec proposition de rapprochement, pour qu'ils ne faussent ni les dépenses ni les revenus.
8. **Historique des imports** et **annulation d'un import** (suppression des lignes qu'il a créées).
9. **Pointage et rapprochement du solde** (fonction unique, voir aussi section 5) : après import, proposer de saisir le solde réel affiché par la banque, afficher l'écart avec le solde calculé, et ouvrir directement la vue de pointage rapide (section 5) pour repérer les oublis ou erreurs.
10. Robustesse : fichier volumineux (10 000 lignes), lignes malformées signalées sans faire échouer tout l'import, limite de taille d'upload configurable (défaut 10 Mo).

---

## 5. Fonctionnalité B : Saisie manuelle

- Formulaire rapide (date, compte, libellé, montant, catégorie, tags, note), utilisable au clavier et sur mobile.
- **Saisie ultra-rapide** : modèles de saisie fréquents (`quick_templates`, montant et catégorie préenregistrés, un clic pour créer), bouton « dupliquer la dernière opération », saisie vocale via la reconnaissance du navigateur quand elle est disponible (traitement local au navigateur, sans service tiers imposé).
- **Vue de pointage rapide** : liste des opérations non pointées, pointage d'un geste (case ou balayage sur mobile), compteur d'écart avec le solde bancaire saisi. Elle remplace tout autre mécanisme de vérification.
- **Recherche et corrections en masse** : après une recherche (par exemple un commerçant), action « recatégoriser/étiqueter toutes les opérations trouvées » avec option de créer en même temps la règle de catégorisation (section 4, point 6) et de l'appliquer rétroactivement à l'historique.
- **Tags** (étiquettes libres, ex. « vacances 2027 », « déductible »), en complément des catégories : ajout/retrait en masse, filtre, rapport par tag.
- **Pièces jointes** : photo ou PDF d'un ticket/facture rattaché à une opération. Les images sont **réduites et recompressées côté serveur** (largeur max 1600 px, JPEG/WebP qualité ~75), stockées dans `/data/attachments/`, limite de taille et quota configurables.
- Saisie d'un virement entre comptes (crée deux écritures liées).
- Modification, suppression et duplication d'une opération ; l'édition par lot (catégorie, compte, tags) s'applique à toute sélection ou résultat de recherche, via le mécanisme unique décrit ci-dessus.
- Liste paginée avec **recherche plein texte**, filtres (compte, période, catégorie, montant min/max, pointé ou non) et tri.
- Opérations **futures / planifiées** autorisées (elles alimentent le prévisionnel).

---

## 6. Fonctionnalité C : Budget prévisionnel à date choisie

C'est la fonction centrale. Elle doit être déterministe, explicable et rapide.

1. **Détection automatique des récurrences** à partir de l'historique : regrouper les opérations par libellé normalisé et montant proche, repérer la périodicité et la régularité (tolérance de ±3 jours, ±10 % sur le montant), calculer un score de confiance. Présenter les candidats dans un écran « Récurrences détectées » où l'utilisateur **valide, modifie ou rejette** chacun. Seules les règles validées entrent dans le prévisionnel. Création manuelle possible.
2. **Entrée** : un compte (ou tous les comptes courants) et une **date cible**.
3. **Calcul** : solde actuel (selon opérations pointées ou non, au choix) + toutes les occurrences futures des règles actives jusqu'à la date cible + opérations planifiées manuelles. Les dépenses variables non récurrentes sont estimées via une **moyenne glissante par catégorie** (paramétrable : 3 / 6 / 12 mois, option activable ou non, affichée à part).
4. **Sortie** :
   - solde projeté à la date cible ;
   - **courbe du solde jour par jour** avec mise en évidence du **point bas** et des périodes où le solde passe sous un seuil d'alerte (paramétrable, défaut 0 €) ;
   - tableau détaillé des occurrences futures (date, libellé, montant, solde cumulé), avec la possibilité d'**exclure ponctuellement** une occurrence ; les simulations « et si » passent par le **moteur de scénarios** unique décrit en section 7 bis.D (pas de mécanisme parallèle) ;
   - total prévisionnel des revenus, dépenses récurrentes et dépenses variables estimées.
5. Prise en compte des jours non ouvrés (règle paramétrable : prélèvement le jour ouvré suivant ou précédent).
6. Les comptes d'épargne peuvent être inclus : intérêts projetés (section 7) et déblocages de fonds.
7. Le calcul ne doit pas dépasser **500 ms** pour un horizon de 24 mois sur 5 ans d'historique, sur le matériel cible.

---

## 7. Fonctionnalité D : Gestion de l'épargne

1. **Création d'un compte d'épargne depuis l'interface** (type, nom, solde initial, banque) sans toucher au code, avec des **modèles préremplis** (Livret A, LDDS, LEP, PEL, livret bancaire, assurance-vie, compte à terme, autre). Les taux et plafonds préremplis ne sont que des **valeurs par défaut modifiables** : ne les présente pas comme officiels et affiche leur date de dernière mise à jour. N'invente aucune valeur que tu ne peux pas vérifier ; en cas de doute, laisse le champ vide.
2. **Intérêts facultatifs** : taux annuel optionnel ; méthodes `aucun`, `simple`, `composé annuel`, et `quinzaine` (capitalisation au 31/12 sur la base des quinzaines, comme un livret réglementé). Génération automatique, à la demande ou au 31/12, des écritures d'intérêts (source `interets`), toujours **modifiables** si elles diffèrent du montant réel de la banque.
3. **Durée de blocage / déblocage des fonds** : durée en mois ou date de déblocage, calcul du **montant disponible vs bloqué** à une date donnée, avertissement avant un retrait ou virement depuis un compte bloqué, pénalité éventuelle paramétrable.
4. **Versements programmés** (virement mensuel du courant vers l'épargne) gérés comme règles récurrentes.
5. **Objectifs et projets d'épargne** : gérés par le module unique « Projets » de la section 7 bis.E (il remplace un simple objectif chiffré).
6. **Vue Épargne** : total épargné, détail par compte (disponible / bloqué / intérêts cumulés), évolution dans le temps, projection à une date choisie.

---

## 7 bis. Fonctionnalités complémentaires (budget, alertes, projets, rapports)

**Règle anti-doublon** : chaque besoin n'est couvert que par **un seul mécanisme**. Les fonctions ci-dessous **réutilisent** les briques existantes (opérations, `recurring_rules`, comptes d'épargne, moteur de prévisionnel, règles de catégorisation) et n'en créent pas de parallèles. Arbitrages déjà faits : pointage unique (section 5), scénarios « et si » uniquement en 7 bis.D, objectifs d'épargne uniquement en 7 bis.E, alertes uniquement en 7 bis.B, exports uniquement en 7 bis.H.

### A. Budgets par catégorie et enveloppes mensuelles
- Plafond mensuel par catégorie (ou groupe de catégories), jauge « consommé / restant » avec code couleur et libellé textuel (pas de couleur seule).
- Report optionnel du reste (ou du dépassement) d'un mois sur l'autre, par enveloppe.
- **Reste à vivre** : revenus attendus du mois − charges fixes (récurrences actives) − versements d'épargne prévus − dépenses déjà faites. Affiché sur le tableau de bord, sans doublonner le solde projeté du prévisionnel (il en reprend les données).
- Vue **budget vs réel** par mois, avec tendance sur 12 mois (c'est ce graphique qui sert de comparaison dans les rapports 7 bis.C).
- Les dépenses variables estimées du prévisionnel (section 6, point 3) utilisent les budgets quand ils existent, sinon la moyenne glissante.

### B. Alertes (système central unique)
- Types : dépassement ou risque de dépassement d'enveloppe ; solde prévisionnel sous le seuil (reprend le seuil de la section 6) ; opération inhabituelle (montant très supérieur à la norme du tiers/catégorie) ; abonnement dont le montant ou la date change ; échéance proche ; import non effectué depuis N jours ; déblocage de fonds d'épargne imminent.
- Chaque type est activable, avec seuils paramétrables par utilisateur. Alertes visibles dans l'application (cloche + page dédiée), acquittables.
- **Canaux sortants facultatifs, désactivés par défaut** : e-mail (SMTP configurable) et webhook/ntfy. Seule exception documentée à la règle « aucun appel sortant » de la section 8, uniquement si l'utilisateur l'active. Récapitulatif périodique (quotidien/hebdomadaire) optionnel. Aucun libellé ni montant détaillé dans les notifications si l'option « notifications discrètes » est cochée.
- Les alertes sont calculées après un import, une saisie ou par une tâche planifiée légère (une fois par jour), jamais par polling.

### C. Suivi des abonnements et rapports
- **Vue Abonnements** : simple vue filtrée des `recurring_rules` marquées « abonnement » (détectées en section 6 ou ajoutées à la main), avec coût mensuel et annuel total, date du prochain prélèvement, détection des doublons possibles (même tiers, montants proches) et des abonnements « oubliés » (peu de variation de la catégorie, pas de nouveau signal d'usage). Pas de seconde détection : on réutilise celle de la section 6.
- **Rapports mensuel et annuel** : dépenses par catégorie, évolution d'un mois sur l'autre, taux d'épargne, top des commerçants, rapport par tag. Exportables (7 bis.H). Le tableau de bord (section 9) reste la synthèse rapide, les rapports sont le détail.
- **Comptes partagés** : chaque membre du foyer (`household_members`, distinct d'un compte utilisateur) peut être « payeur » d'une opération ; répartition configurable (50/50, au prorata des revenus, montants libres) via `transaction_splits` ; écran **« qui doit combien »** avec solde net entre membres et enregistrement d'un remboursement (qui génère un virement interne, section 4, point 7).

### D. Scénarios « et si » (moteur unique)
- Un scénario est un **calque nommé** de modifications appliquées au prévisionnel sans toucher aux données réelles : dépense/revenu ponctuel ou récurrent ajouté, règle suspendue (arrêt d'un abonnement), baisse ou hausse d'un revenu (% ou montant), nouveau crédit, versement d'épargne modifié.
- Scénarios **enregistrables et nommés**, comparaison côte à côte avec le scénario de référence (solde à la date cible, point bas, écart cumulé), conversion d'un scénario en données réelles en un clic.
- Option d'inflation estimée (phase 2) applicable à un scénario ou au prévisionnel.

### E. Projets et objectifs (module unique)
- Un projet (vacances, travaux, voiture…) a un montant cible, une date, un **compte d'épargne de rattachement** et une **cagnotte virtuelle** : la part de l'épargne affectée au projet est suivie sans créer de compte bancaire supplémentaire ; un compte peut porter plusieurs projets.
- Calcul du **versement mensuel suggéré**, de la date d'atteinte projetée au rythme actuel, et progression visuelle. Possibilité de créer le versement programmé correspondant (règle récurrente, section 7, point 4).
- Les dépenses du projet peuvent être rattachées par tag.

### F. Dépenses annuelles lissées et crédits
- **Lissage** : sur une règle récurrente annuelle (assurance, impôts, cadeaux…), option « provisionner chaque mois 1/12 » : le prévisionnel et le reste à vivre intègrent la provision mensuelle, qui s'alimente d'un compte d'épargne ou d'une enveloppe dédiée, et se solde à l'échéance.
- **Suivi de crédits** : capital emprunté, taux, durée, mensualité, assurance, date de début ; **tableau d'amortissement** (capital restant dû, intérêts payés, date de fin), remboursements anticipés simulables (via un scénario). Le crédit génère automatiquement la règle récurrente de la mensualité (pas de saisie en double) et apparaît dans le prévisionnel.

### G. Rappels et notifications
- Rappels d'échéances et de l'import hebdomadaire passent par le système d'alertes (7 bis.B) et ses canaux optionnels. Pas de moteur de rappel séparé.

### H. Exports
- Point d'entrée unique « Exports » : opérations (CSV), rapports (CSV/PDF), exports pour la déclaration d'impôts ou un comptable (par période, tag « déductible » ou catégorie, avec pièces jointes en archive ZIP optionnelle). Génération en flux pour limiter la mémoire ; PDF produits par une bibliothèque Go légère, sans navigateur embarqué.

### I. Phase 2 (après la v1, architecture à prévoir dès maintenant)
- **Imports OFX/QIF et PDF** : nouveaux parseurs branchés sur la même interface de profils d'import (section 4). Le PDF passe par une extraction de texte simple, avec prévisualisation obligatoire.
- **Prévisionnel enrichi d'une inflation estimée** (paramètre global et par catégorie).
- **Suivi du patrimoine** : biens (immobilier, véhicules, placements) avec valeur saisie ou revalorisée manuellement, valeur nette globale = comptes + patrimoine − crédits (réutilise 7 bis.F).
- La sauvegarde chiffrée vers un second emplacement est déjà couverte en section 8 (livrée en v1).

### Contraintes de performance communes
Toutes ces fonctions respectent les budgets de la section 2 : calculs à la demande ou mis en cache dans SQLite (tables de synthèse mensuelles recalculées de manière incrémentale), aucune tâche de fond permanente, tâche planifiée quotidienne légère unique.

---

## 8. Sécurité, sauvegarde, exploitation

- **Authentification** locale obligatoire : comptes utilisateurs, mots de passe hachés (argon2id avec paramètres modérés adaptés à un CPU faible, ou bcrypt), sessions par cookies `HttpOnly` + `SameSite=Lax`, protection CSRF, limitation des tentatives de connexion. Assistant de création du premier compte au premier lancement.
- Prévu pour fonctionner derrière le **reverse proxy Synology** en HTTPS ; paramètre `BASE_URL` / `TRUSTED_PROXIES`. Aucun appel sortant par défaut (pas de télémétrie, pas de CDN, pas de polices distantes) ; seules exceptions, désactivées par défaut et activables par l'utilisateur : notifications e-mail/webhook (7 bis.B) et copie de sauvegarde externe.
- Les données bancaires sont sensibles : **ne journalise jamais** de libellés ni de montants en niveau de log normal.
- **Sauvegarde** : bouton d'export (sauvegarde cohérente SQLite via `VACUUM INTO` ou l'API de backup), sauvegarde automatique planifiée optionnelle avec rotation vers `/data/backups/`, **chiffrement optionnel** de l'archive (mot de passe, `age` ou AES-GCM) et **copie vers un second emplacement** configurable (dossier monté, SFTP ou WebDAV, désactivé par défaut), procédure de restauration documentée. **Exports** (point d'entrée unique, voir 7 bis.H) : CSV de toutes les opérations, exports fiscaux/comptables CSV et PDF.
- Configuration par variables d'environnement (`PORT`, `DATA_DIR`, `TZ`, `LOG_LEVEL`, `MAX_UPLOAD_MB`, etc.), sans fichier de config obligatoire.
- Endpoint `/healthz`. Logs structurés sur stdout.

---

## 9. Interface utilisateur

**Exigences obligatoires d'ergonomie (non négociables) :**
- **Interface ergonomique** : parcours courts (saisie d'une opération en moins de 15 s, import en 3 étapes), actions fréquentes accessibles en 1 à 2 clics, libellés clairs en français, messages d'erreur compréhensibles, confirmations avant toute suppression, états de chargement et retours visuels après chaque action.
- **Mode clair ET mode sombre** : les deux sont obligatoires, couvrent 100 % des écrans et des graphiques. Par défaut, suivre `prefers-color-scheme`. Un **sélecteur manuel** (Auto / Clair / Sombre) est accessible depuis l'en-tête ou les paramètres, avec le choix mémorisé (par utilisateur) et appliqué sans flash au chargement. Contrastes conformes WCAG AA dans les deux thèmes ; les thèmes reposent sur des variables CSS.
- **Responsive, « mobile-first »** : l'application doit être **facilement utilisable sur téléphone** (à partir de 360 px de large) comme sur tablette et ordinateur, sans défilement horizontal de la page.
  - Navigation adaptée au mobile : barre d'onglets en bas ou menu compact, bouton d'action flottant « + Opération » toujours accessible.
  - Cibles tactiles d'au moins 44×44 px, champs de formulaire de taille ≥ 16 px (pas de zoom automatique sur iOS), claviers adaptés (`inputmode="decimal"` pour les montants, sélecteur de date natif).
  - Les tableaux d'opérations se transforment en **liste de cartes** sur petit écran ; les filtres s'ouvrent dans un panneau repliable ; les graphiques s'adaptent à la largeur et restent lisibles.
  - Le flux d'import et le prévisionnel doivent rester utilisables au pouce sur mobile.
  - Balise `viewport` correcte et **manifeste PWA** léger (ajout à l'écran d'accueil, icône, thème), sans service worker lourd ni mode hors-ligne obligatoire.
- Tests : captures ou tests Playwright à 360 px, 768 px et 1280 px, en clair et en sombre, pour les écrans clés ; audit Lighthouse (accessibilité ≥ 90) consigné dans `docs/`.

**Autres exigences :** sobre, rapide, accessible (navigation clavier, libellés de formulaires, focus visible).
- Écrans : **Tableau de bord** (soldes par compte, reste à vivre, enveloppes, alertes), **Opérations** (dont pointage rapide), **Importer**, **Récurrences et abonnements**, **Budgets**, **Prévisionnel et scénarios**, **Épargne et projets**, **Crédits**, **Partage du foyer**, **Rapports**, **Alertes**, **Exports**, **Catégories, tags et règles**, **Paramètres** (comptes, utilisateurs, membres du foyer, notifications, sauvegardes). Sur mobile, regrouper les écrans secondaires dans un menu « Plus » pour garder une barre d'onglets à 4-5 entrées.
- Graphiques légers (barres par catégorie, courbe de solde), avec alternative tabulaire.
- Aucune dépendance front nécessitant une étape de build Node côté production. Un éventuel outil de build CSS reste facultatif et n'est pas requis au runtime.

---

## 10. Qualité, tests, documentation

- **Tests unitaires** (obligatoires) : parseurs de chaque profil, normalisation des libellés, déduplication, détection de récurrences, moteur de prévisionnel (cas : fin de mois, année bissextile, jours non ouvrés, règles qui se terminent), calcul d'intérêts (simple, composé, quinzaine), logique de blocage.
- **Tests d'intégration** : parcours import → catégorisation → prévisionnel sur des fixtures, avec base SQLite temporaire.
- **Benchmarks** : import de 10 000 lignes et calcul du prévisionnel à 24 mois ; consigner les résultats et l'empreinte mémoire dans `docs/PERFORMANCE.md`.
- CI GitHub Actions : `go vet`, `staticcheck`/`golangci-lint`, tests, build multi-arch.
- Documentation : `README.md` (installation sur Synology pas à pas avec Container Manager, variables d'environnement, sauvegarde/restauration, ajout d'un profil d'import), `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`.
- Licence : à me proposer (MIT ou AGPL) ; données de test **anonymisées**.

---

## 11. Jalons proposés

1. **Socle** : dépôt, Dockerfile multi-arch, SQLite + migrations, auth, gabarit d'interface, CI, `/healthz`.
2. **Comptes, catégories et saisie manuelle** (section 5).
3. **Import CSV/XLSX/XLS** avec profils SG, BNP, générique, déduplication, prévisualisation, annulation (section 4).
4. **Règles de catégorisation et virements internes.**
5. **Récurrences** : détection, validation, création manuelle (section 6, étapes 1 à 2).
6. **Prévisionnel** à date choisie, courbe, point bas, seuil (section 6).
7. **Épargne** : comptes, intérêts, blocage, versements programmés (section 7, points 1 à 4 et 6).
8. **Budgets par enveloppe, reste à vivre et système d'alertes** (7 bis.A, 7 bis.B), avec canaux optionnels.
9. **Abonnements, rapports, tags, pièces jointes, saisie rapide, pointage** (7 bis.C, section 5).
10. **Scénarios « et si », projets/objectifs, lissage annuel, crédits** (7 bis.D, E, F).
11. **Comptes partagés, exports, sauvegarde chiffrée et copie externe** (7 bis.C, H, section 8).
12. **Durcissement, mesures de performance, documentation finale** (v1 terminée).
13. **Phase 2** : imports OFX/QIF/PDF, inflation, patrimoine (7 bis.I).

## 12. Critères d'acceptation globaux

- [ ] L'image démarre sur `amd64` et `arm64`, pèse moins de 50 Mo et consomme moins de 64 Mo de RAM au repos (mesure documentée).
- [ ] Un export réel SG et un export réel BNP s'importent sans doublon, même réimportés ou chevauchants.
- [ ] Je peux saisir, modifier et supprimer des opérations, y compris des virements internes.
- [ ] Le prévisionnel affiche à une date choisie un solde projeté explicable ligne par ligne, avec le point bas et les alertes.
- [ ] Je peux créer un compte d'épargne dans l'interface, avec ou sans taux, avec une durée de blocage, et voir le disponible, le bloqué et les intérêts projetés.
- [ ] L'interface fonctionne en mode clair et sombre (auto + choix manuel) et reste pleinement utilisable sur un téléphone de 360 px de large, sans défilement horizontal, pour la saisie, l'import, le prévisionnel et l'épargne.
- [ ] Budgets par enveloppe, reste à vivre, alertes, abonnements, rapports, scénarios, projets, lissage, crédits, comptes partagés, tags, pièces jointes et exports fonctionnent et réutilisent les mêmes briques (aucune fonction en double, vérifié en revue de code).
- [ ] Les nouvelles fonctions respectent les budgets mémoire/CPU de la section 2 (mesures mises à jour dans `docs/PERFORMANCE.md`).
- [ ] Aucune requête réseau sortante par défaut (seuls l'e-mail/webhook et la copie de sauvegarde, désactivés par défaut, peuvent en émettre) ; authentification active ; sauvegarde et restauration vérifiées.
- [ ] Tous les tests passent et la documentation d'installation Synology est utilisable de bout en bout.

## 13. Hors périmètre (v1)

Connexion directe aux banques (Open Banking / agrégation), multi-devises, application mobile native, multi-foyers. (Les imports OFX/QIF/PDF, l'inflation et le patrimoine sont en phase 2, voir 7 bis.I.)

---

**Commence maintenant par la section 0 : plan, structure du dépôt et questions bloquantes.**
