// Package validation porte l'erreur de saisie commune à tous les domaines :
// son message, en français, s'adresse directement à l'utilisateur.
package validation

import (
	"errors"
	"fmt"
)

// Error est une saisie refusée ; le message s'affiche tel quel.
type Error string

func (e Error) Error() string { return string(e) }

// Errorf construit une erreur de saisie à partir d'un format.
func Errorf(format string, a ...any) Error { return Error(fmt.Sprintf(format, a...)) }

// Message renvoie le message à afficher si err est une erreur de saisie.
func Message(err error) (string, bool) {
	var e Error
	if errors.As(err, &e) {
		return string(e), true
	}
	return "", false
}
