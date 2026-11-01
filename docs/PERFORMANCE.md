# Mesures de performance

Cibles (cahier des charges, section 2) : RAM au repos < 64 Mo (objectif
30 Mo), image < 50 Mo, démarrage < 3 s, pages < 150 Ko hors graphiques.

## Jalon 1 : 31/10/2026

Binaire `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`, Go 1.27.3,
linux/amd64, machine de développement (Xeon 2,1 GHz, 4 vCPU), base vierge.
Le NAS cible est plus lent : les durées seront à remesurer sur place, les
volumes mémoire sont comparables.

| Mesure | Résultat | Cible |
|---|---|---|
| Taille du binaire (= contenu de l'image `scratch`) | 14,7 Mo | image < 50 Mo |
| Démarrage (ouverture de la base + migrations + écoute) | 11 à 22 ms | < 3 s |
| RAM au repos après démarrage (RSS) | 13 Mo | < 64 Mo (objectif 30) |
| RAM au repos après création du compte et 4 connexions | 16 Mo | < 64 Mo |
| RAM au repos après 10 connexions simultanées | 17 Mo | < 64 Mo |
| Pic pendant 10 connexions simultanées | 56 Mo | < 128 Mo (`mem_limit`) |
| Durée d'une connexion (argon2id 19 Mio, t=2) | ≈ 50 ms | — |
| Page de connexion (HTML) | 1,7 Ko | < 150 Ko |
| Tableau de bord (HTML) | 3,0 Ko | < 150 Ko |
| Feuille de style | 6,6 Ko (2,2 Ko compressée) | — |

La taille de l'image et sa mémoire au repos, mesurées dans le conteneur
lui-même (limites `--memory 128m --cpus 0.5`), figurent dans le résumé de
chaque exécution de la CI (tâche « Image Docker »).

### Correction apportée pendant la mesure

Avant correction, la RAM au repos restait à **55 Mo** plusieurs minutes après
une connexion : le tampon de travail d'argon2id (19 Mio) devenait inutile,
mais aucune allocation ne déclenchait de ramasse-miettes au repos. Il est
désormais rendu au système dès la fin du calcul (voir `DECISIONS.md`).

## Jalon 2 : 31/10/2026

Même machine. Benchmarks `go test -bench . ./internal/ledger/` sur
**20 000 opérations** (environ cinq ans d'un foyer actif) :

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Liste sans critère (page de 50 + nombre + total) | 1,7 ms | 54 Ko |
| Recherche plein texte « cafe gare » (préfixes, sans accents) | 9,1 ms | 55 Ko |
| Filtre période + montant minimal | 1,3 ms | 55 Ko |
| Soldes de tous les comptes (index couvrant) | 2,3 ms | 2 Ko |

Binaire (avec HTMX et la base des fuseaux) et serveur :

| Mesure | Résultat | Cible |
|---|---|---|
| Taille du binaire | 15,1 Mo | image < 50 Mo |
| Démarrage | 27 ms | < 3 s |
| RAM au repos après redémarrage (RSS) | 17,5 Mo | < 64 Mo (objectif 30) |
| RAM après 200 saisies et la visite de chaque écran | 21 Mo | < 64 Mo |
| Page Opérations (50 lignes, HTML) | 42 Ko | < 150 Ko |
| Page de saisie (HTML) | 10 Ko | < 150 Ko |
| HTMX (compressé, mis en cache un an) | 17 Ko | — |
| Feuille de style (compressée) | 4,2 Ko | — |

Le parcours de saisie demande quatre gestes (bouton « + Opération »,
montant, Tab et libellé, Entrée) ; le test de bout en bout le joue au clavier
seul et vérifie le retour à la page de départ avec le solde à jour.

## Jalon 3 : 31/10/2026

Même machine. Import d'un relevé de **10 000 lignes**, mesuré sur le binaire
(dépôt par le formulaire, aperçu, validation) :

| Mesure | CSV (770 Ko) | XLSX (276 Ko) | Cible |
|---|---|---|---|
| Dépôt, lecture et mise en attente | 0,14 s | 0,38 s | — |
| Validation (insertion et indexation plein texte) | 0,16 s | 0,16 s | — |
| Pic de RAM du processus (`VmHWM`) | 41 Mo | 48 Mo | < 150 Mo |
| RAM juste après la validation | 29 Mo | 31 Mo | < 64 Mo |

Benchmark `go test -bench Import10000 ./internal/importer/` (dépôt, mise en
attente et validation, en mémoire) : 240 ms et 15 Mo alloués au total par
import.

| Mesure | Résultat | Cible |
|---|---|---|
| Taille du binaire (avec excelize, xls, YAML) | 18,3 Mo | image < 50 Mo |
| RAM au repos après démarrage | 21 à 23 Mo | < 64 Mo (objectif 30) |

### Correction apportée pendant la mesure

Sans précaution, la RAM restait à 55 Mo après un import de 10 000 lignes,
et ne redescendait qu'à 49 Mo après plusieurs minutes : le tas Go n'en
représentait que 9 Mo, le reste étant le cache et les tampons de SQLite.
`db.ReleaseMemory` (cache SQLite de la connexion d'écriture et tas Go rendus
au système) est appelé après la mise en attente, la validation et
l'annulation d'un import.

## Jalon 4 : 31/10/2026

Même machine, **20 000 opérations** :

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Application de 50 règles (dont 5 expressions régulières) à toutes les opérations | 78 ms | 18 Mo |
| Recherche des virements internes possibles (1 000 crédits candidats) | 19 ms | 0,2 Mo |

La recherche des virements est refaite à chaque affichage complet de la liste
des opérations (pas lors des mises à jour de la recherche instantanée) ; sa
durée sur le NAS reste à mesurer (jalon 12).

## Jalon 5 : 31/10/2026

Même machine. Détection des récurrences sur **20 000 opérations** (trois ans,
60 séries mensuelles mêlées à des achats sans régularité) : **49 ms**, 12 Mo
alloués. La première version évaluait les six périodicités pour chaque série
(135 ms) ; n'évaluer que celles proches de l'écart médian entre occurrences
a divisé le temps par près de trois sans changer les résultats.

## Jalon 6 : 31/10/2026

Même machine. Prévisionnel à **24 mois** sur deux comptes courants riches de
**cinq ans d'historique** (20 000 opérations, aucune pointée) et de 30
récurrences (hebdomadaires, mensuelles, trimestrielles), avec le cas le plus
coûteux : moyenne des dépenses variables sur 12 mois et solde pointé.

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Calcul complet (lecture en base comprise), 731 jours, ≈ 1 650 lignes | 27 ms | 2,2 Mo |

L'objectif du cahier des charges est de 500 ms sur le NAS ; la marge est de
l'ordre de 18 fois sur cette machine, à confirmer sur le matériel cible
(jalon 12). La page (courbe SVG comprise) ne pèse que quelques dizaines de
kilo-octets avant compression.

## Jalon 7 : 31/10/2026

Le prévisionnel à 24 mois du jalon 6 reste à **28 ms** (2,5 Mo alloués)
avec la recherche des intérêts estimés. Les calculs d'épargne portent sur
les seuls mouvements des comptes d'épargne (quelques centaines au plus) :
365 jours ou 24 quinzaines par année calculée, négligeable. La tâche
quotidienne ne consomme rien entre deux passages (un minuteur, aucune
interrogation périodique).

## Jalon 8 : 31/10/2026

Même machine, cinq ans d'historique (20 000 opérations), 30 récurrences,
10 enveloppes avec report :

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Examen complet des sept types d'alerte (dont un prévisionnel à 30 jours) | 44 ms | 7 Mo |

L'examen a lieu en arrière-plan après une modification (les demandes
rapprochées n'en font qu'une) et chaque nuit : il ne ralentit aucune page.
La cloche de l'en-tête ajoute un décompte indexé par page affichée.

## Jalon 9 : 31/10/2026

Même machine :

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Réduction d'une photo de 12 Mpx (4000 × 3000) à 1 600 px, JPEG q75 | 260 ms | 38 Mo |
| Rapport annuel sur cinq ans d'historique (20 000 opérations) | 32 ms | 7 Mo |

### Correction apportée pendant la mesure
La première version de la réduction des images utilisait le noyau bilinéaire
de `x/image/draw` : 180 Mo alloués par photo, à cause d'un tampon de
flottants de la largeur cible par la hauteur source, intenable sous la
limite de 128 Mo du conteneur. La moyenne de blocs suivie d'une
interpolation sans tampon ramène l'allocation à 38 Mo (la photo décodée,
l'image intermédiaire et le résultat) et le temps de 470 à 260 ms ; une
seule image est traitée à la fois.

## Jalon 10 : 01/11/2026

Même machine, même foyer chargé (cinq ans d'historique, 30 récurrences,
moyenne sur 12 mois, solde pointé), horizon de 24 mois :

| Opération | Durée | Mémoire allouée |
|---|---|---|
| Prévisionnel seul | 35 ms | 2,7 Mo |
| Comparaison d'un scénario de six modifications avec la référence (trois dépenses lissées, un crédit) | 73 ms | 6 Mo |

La comparaison calcule deux prévisionnels complets ; le scénario lui-même
(changements datés des récurrences, mensualités simulées, tableau
d'amortissement d'un remboursement anticipé) ne pèse presque rien à côté de
la lecture en base. Un tableau d'amortissement compte 600 lignes au plus.

## Méthode

```bash
go test -run '^$' -bench . -benchmem ./internal/ledger/ ./internal/importer/ ./internal/categorize/ ./internal/recurring/ ./internal/forecast/ ./internal/alerts/ ./internal/reports/ ./internal/attachments/

make build
DATA_DIR=/tmp/bg PORT=8080 ./bin/buddyget &
grep -E 'VmRSS|VmHWM' /proc/$!/status   # RSS courant et pic
```

Pour les connexions simultanées, 10 requêtes `curl -d … /login` lancées en
parallèle, puis lecture de `VmHWM` (pic) et de `VmRSS` deux secondes après.
