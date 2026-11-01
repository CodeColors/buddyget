package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Paramètres argon2id : minimum recommandé par l'OWASP (19 Mio, 2 passes,
// 1 fil), adapté à un CPU de NAS.
const (
	argonMemory  = 19 * 1024 // Kio
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots borne le nombre de calculs argon2id simultanés : chacun alloue
// 19 Mio, ce qui doit rester compatible avec la limite mémoire du conteneur.
var hashSlots = make(chan struct{}, 2)

var b64 = base64.RawStdEncoding

// hashPassword renvoie l'empreinte au format PHC :
// $argon2id$v=19$m=19456,t=2,p=1$<sel>$<empreinte>.
func hashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := deriveKey(ctx, password, salt, argonTime, argonMemory, argonThreads)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// verifyPassword compare en temps constant le mot de passe à une empreinte
// PHC. Les paramètres sont relus depuis l'empreinte, ce qui permet de les
// faire évoluer sans invalider les comptes existants.
func verifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errors.New("empreinte de mot de passe illisible")
	}
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil {
		return false, fmt.Errorf("paramètres argon2id illisibles : %w", err)
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	if passes < 1 || threads < 1 || len(want) != argonKeyLen {
		return false, errors.New("paramètres argon2id invalides")
	}
	got, err := deriveKey(ctx, password, salt, passes, memory, threads)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func deriveKey(ctx context.Context, password string, salt []byte, passes, memory uint32, threads uint8) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	key := argon2.IDKey([]byte(password), salt, passes, memory, threads, argonKeyLen)
	// Le tampon de travail (19 Mio) est rendu au système immédiatement : au
	// repos, aucun GC ne le libérerait avant plusieurs minutes, soit plus du
	// triple de l'empreinte mémoire normale. Coût : un GC (~1 ms) par calcul.
	debug.FreeOSMemory()
	return key, nil
}
