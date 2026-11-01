# Accessibilité

Exigence du cahier des charges : audit Lighthouse, accessibilité ≥ 90,
contrastes WCAG AA dans les deux thèmes, navigation au clavier, libellés de
formulaires, focus visible.

## Audit Lighthouse : 01/11/2026 (v1.0)

Lighthouse 12.8.2, catégorie accessibilité, profil mobile (412 px),
Chromium sans interface, sur le binaire réel avec un foyer de démonstration
(comptes courant et d'épargne, opérations, récurrences, enveloppe, membres,
crédit, projet, scénario, sauvegarde). Chaque écran est audité deux fois :
utilisateur réglé en thème clair, puis en thème sombre.

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
| Partage du foyer (`/partage`) | 100 | 100 |
| Crédit (`/credits/1`) | 100 | 100 |
| Projets (`/projets`) | 100 | 100 |
| Scénario (`/scenarios/1`) | 100 | 100 |
| Exports (`/exports`) | 100 | 100 |
| Sauvegardes (`/sauvegardes`) | 100 | 100 |
| Plus (`/plus`) | 100 | 100 |

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
  dans les deux thèmes ; le choix (Auto, Clair, Sombre) est appliqué sans
  flash.
- Lien « Aller au contenu », focus visible (contour de 3 px), libellé pour
  chaque champ, messages d'erreur en `role="alert"`, confirmations en
  `role="status"`.
- Cibles tactiles de 44 × 44 px au moins, champs de 16 px au moins (pas de
  zoom automatique sur iOS) ; vérifiés par les tests de bout en bout.
- Graphiques en SVG avec description textuelle et tableau équivalent.
- Les fonctions essentielles marchent sans JavaScript ; HTMX et les petits
  scripts accélèrent la saisie (la dictée vocale, seule, en dépend).
