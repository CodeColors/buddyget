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

## Méthode

```bash
make build
DATA_DIR=/tmp/bg PORT=8080 ./bin/buddyget &
grep -E 'VmRSS|VmHWM' /proc/$!/status   # RSS courant et pic
```

Pour les connexions simultanées, 10 requêtes `curl -d … /login` lancées en
parallèle, puis lecture de `VmHWM` (pic) et de `VmRSS` deux secondes après.
