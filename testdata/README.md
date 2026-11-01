# Fichiers d'exemple pour les tests d'import

**Tous ces fichiers sont synthétiques.** Aucun export réel de la Société
Générale ni de BNP Paribas n'a été fourni à ce jour : leur structure
(préambule, intitulés de colonnes, encodage) est plausible mais **non
vérifiée**. Les profils `internal/importer/profiles/sg.yaml` et `bnp.yaml`
sont marqués provisoires pour la même raison.

Dès que des exports réels anonymisés sont disponibles, ils remplacent ces
fichiers, et les profils sont ajustés si besoin (intitulés de colonnes,
format de date).

| Fichier | Contenu |
|---|---|
| `sg/releve-octobre.csv` | Windows-1252, séparateur `;`, préambule de deux lignes, deux achats identiques le même jour |
| `sg/releve-chevauchant.csv` | export suivant qui reprend le 31/10 : le réimport ne doit rien ajouter en double |
| `bnp/releve-octobre.xlsx` | ligne de titre avant l'en-tête, dates et montants en cellules numériques |
| `generique/debit-credit.xls` | Excel 97-2003, colonnes Débit et Crédit séparées |
| `generique/sans-entete.csv` | aucun en-tête, séparateur `,`, dates AAAA-MM-JJ : correspondance manuelle nécessaire |

Les classeurs ont été produits par LibreOffice (paramètres régionaux
français) à partir de CSV équivalents.
