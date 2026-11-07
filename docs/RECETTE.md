# Recette de la v1.0

Critères d'acceptation globaux du cahier des charges (section 12), avec la
preuve correspondante. Vérification au 01/11/2026.

| Critère | État | Preuve |
|---|---|---|
| L'image démarre sur `amd64` et `arm64`, pèse moins de 50 Mo et consomme moins de 64 Mo de RAM au repos (mesure documentée). | ✅ | CI, tâche « Image Docker » : taille ≈ 21 Mo, mémoire au repos mesurée dans le conteneur (`--memory 128m --cpus 0.5`), échec au-delà de 50 Mo ou de 64 Mo ; image arm64 démarrée sous émulation et contrôlée par son `healthcheck`. Détail dans [PERFORMANCE.md](PERFORMANCE.md). |
| Un export réel SG et un export réel BNP s'importent sans doublon, même réimportés ou chevauchants. | ✅ | Déduplication par rang d'occurrence testée sur des relevés synthétiques réimportés et chevauchants (`internal/importer`, `testdata/`). Profils SG et BNP vérifiés sur des exports réels (.xls, toutes les opérations lues et reconnues), dont la structure est reproduite sans données personnelles dans `xls_test.go` ; l'assistant de choix des colonnes couvre un format non reconnu. |
| Je peux saisir, modifier et supprimer des opérations, y compris des virements internes. | ✅ | Tests `internal/ledger`, `internal/web` (`ledger_test.go`, `entry_test.go`) ; parcours e2e de saisie au clavier et de virement. |
| Le prévisionnel affiche à une date choisie un solde projeté explicable ligne par ligne, avec le point bas et les alertes. | ✅ | `internal/forecast` (fin de mois, années bissextiles, jours fériés, règles qui se terminent, lissage, scénarios) ; écran Prévisionnel : détail du calcul, échéances, point bas, périodes sous le seuil ; alerte « solde prévu sous le seuil ». |
| Je peux créer un compte d'épargne dans l'interface, avec ou sans taux, avec une durée de blocage, et voir le disponible, le bloqué et les intérêts projetés. | ✅ | `internal/savings` (intérêts simples, composés, par quinzaine ; blocage par date ou par durée) ; parcours e2e « épargne sur mobile ». |
| L'interface fonctionne en mode clair et sombre (auto + choix manuel) et reste pleinement utilisable sur un téléphone de 360 px, sans défilement horizontal, pour la saisie, l'import, le prévisionnel et l'épargne. | ✅ | e2e : chaque écran à 360, 768 et 1280 px, en clair et en sombre, sans défilement horizontal, cibles de 44 px, champs de 16 px, WCAG 2.1 AA vérifié par axe ; Lighthouse accessibilité 100 ([ACCESSIBILITE.md](ACCESSIBILITE.md)). |
| Budgets, reste à vivre, alertes, abonnements, rapports, scénarios, projets, lissage, crédits, comptes partagés, tags, pièces jointes et exports fonctionnent et réutilisent les mêmes briques (aucune fonction en double). | ✅ | Un service par domaine ([ARCHITECTURE.md](ARCHITECTURE.md)) ; un seul moteur de prévisionnel (scénarios, lissage, crédits, reste à vivre), un seul système d'alertes, un seul mécanisme de pointage, un seul point d'entrée pour les exports, les versements des projets et les mensualités des crédits sont des récurrences ([DECISIONS.md](DECISIONS.md)). |
| Les nouvelles fonctions respectent les budgets mémoire/CPU (mesures dans `docs/PERFORMANCE.md`). | ✅ | Bilan v1.0 de [PERFORMANCE.md](PERFORMANCE.md) : pics sous 128 Mo, mémoire rendue au système après les opérations lourdes, aucune tâche de fond permanente. |
| Aucune requête réseau sortante par défaut ; authentification active ; sauvegarde et restauration vérifiées. | ✅ | Seuls les canaux de notification et la copie WebDAV émettent, désactivés par défaut ; aucun CDN, police ni script distant (CSP `default-src 'self'`) ; toutes les pages sauf `/login`, `/setup` et `/healthz` exigent une session ; sauvegarde et restauration testées de bout en bout (`internal/backup`, `internal/web`). |
| Tous les tests passent et la documentation d'installation Synology est utilisable de bout en bout. | ✅ | CI verte (vet, golangci-lint, `go test -race`, Playwright, image multi-architecture) ; [README](../README.md) : installation, HTTPS, premiers pas, mise à jour, sauvegarde, restauration, mot de passe oublié, dépannage. |

## Reste à faire avant la mise en service

- Publier l'image : fusion dans `main` (étiquette `main`) et étiquette de
  version `v1.0.0` (étiquettes `1.0.0`, `1.0` et `latest`), qui déclenchent
  la publication par la CI.
- Remesurer durées et mémoire sur le NAS lui-même (la machine de mesure est
  plus rapide ; les volumes mémoire sont comparables).
