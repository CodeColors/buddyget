# Accessibilité

Exigence du cahier des charges : audit Lighthouse, accessibilité ≥ 90,
contrastes WCAG AA dans les deux thèmes, navigation au clavier, libellés de
formulaires, focus visible.

## Audit Lighthouse : 17/11/2026 (v1.1, interface refondue)

Lighthouse 12.8.2, catégorie accessibilité, profil mobile (412 px),
Chromium sans interface, sur le binaire réel avec un foyer de démonstration
(trois comptes, trois mois d'opérations catégorisées par des règles,
récurrences validées, quatre enveloppes, alertes). Chaque écran est audité
deux fois : utilisateur réglé en thème clair, puis en thème sombre.

| Écran | Clair | Sombre |
|---|---|---|
| Connexion (`/login`) | 100 | — |
| Tableau de bord (`/`) | 100 | 100 |
| Opérations (`/operations`) | 100 | 100 |
| Saisie (`/operations/nouvelle`) | 100 | 100 |
| Import (`/import`) | 100 | 100 |
| Prévisionnel (`/previsionnel`) | 100 | 100 |
| Épargne (`/epargne`) | 100 | 100 |
| Budgets (`/budgets`) | 100 | 100 |
| Rapports (`/rapports`) | 100 | 100 |
| Récurrences (`/recurrences`) | 100 | 100 |
| Abonnements (`/abonnements`) | 100 | 100 |
| Alertes (`/alertes`) | 100 | 100 |
| Partage du foyer (`/partage`) | 100 | 100 |
| Crédits (`/credits`) | 100 | 100 |
| Projets (`/projets`) | 100 | 100 |
| Scénarios (`/scenarios`) | 100 | 100 |
| Exports (`/exports`) | 100 | 100 |
| Sauvegardes (`/sauvegardes`) | 100 | 100 |
| Paramètres (`/parametres`) | 100 | 100 |
| Plus (`/plus`) | 100 | 100 |

L'audit de la v1.0 (01/11/2026, ancienne interface) donnait déjà 100 sur
chaque écran ; les écrans de détail (crédit, scénario) restent couverts par
le contrôle continu ci-dessous.

### Méthode

```bash
make build
DATA_DIR=/tmp/lh PORT=18200 ./bin/buddyget &
# créer un compte et des données, puis, avec le cookie de session :
npx lighthouse@12 http://127.0.0.1:18200/previsionnel --only-categories=accessibility \
  --extra-headers='{"Cookie":"buddyget_session=…"}' --chrome-flags="--headless=new"
```

Le thème se choisit avant l'audit par `POST /preferences/theme`
(`theme=light` ou `theme=dark`).

## Contrôle continu

Les tests de bout en bout (`e2e/`) passent chaque écran à
[axe-core](https://github.com/dequelabs/axe-core), le moteur des audits
d'accessibilité de Lighthouse, avec les règles WCAG 2.1 A et AA, à 360, 768
et 1280 px, en clair et en sombre, ainsi qu'à chaque étape des parcours. Une
violation fait échouer la CI.

Ce contrôle a relevé un défaut que l'audit Lighthouse, à 412 px, ne voyait
pas : à 360 px, le tableau d'amortissement d'un crédit défile en largeur et
n'était pas atteignable au clavier. Les zones de tableau défilantes sont
désormais focalisables et nommées (`role="region"`, `aria-label`).

## Choix de conception

- Thèmes clair et sombre par variables CSS, contrastes AA vérifiés par axe
  dans les deux thèmes ; le choix (Automatique, Clair, Sombre), dans le menu
  du compte, est appliqué sans flash.
- Une seule navigation principale exposée à la fois (barre d'onglets ou
  barre latérale selon la largeur) ; menu du compte en `<details>`,
  utilisable au clavier et sans script, refermé par Échap.
- Graphiques décoratifs (anneaux, pastilles, barres) masqués aux lecteurs
  d'écran : chaque valeur figure aussi en texte (légende, montants).
- Lien « Aller au contenu », focus visible (contour de 3 px, halo sur les
  champs), libellé pour chaque champ, messages d'erreur en `role="alert"`,
  confirmations en `role="status"`.
- Cibles tactiles de 44 × 44 px au moins, champs de 16 px au moins (pas de
  zoom automatique sur iOS) ; vérifiés par les tests de bout en bout.
- Graphiques en SVG avec description textuelle et tableau équivalent.
- Les fonctions essentielles marchent sans JavaScript ; HTMX et les petits
  scripts accélèrent la saisie (la dictée vocale, seule, en dépend).
