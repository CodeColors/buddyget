// Package config lit la configuration de BuddyGet depuis les variables
// d'environnement. Aucun fichier de configuration n'est nécessaire.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Config regroupe les paramètres d'exécution validés.
type Config struct {
	Port     int
	DataDir  string
	LogLevel slog.Level
	// BaseURL est l'adresse publique (derrière le reverse proxy), facultative.
	BaseURL *url.URL
	// TrustedProxies liste les réseaux dont l'en-tête X-Forwarded-For est cru.
	TrustedProxies []netip.Prefix
	// MaxUploadMB borne la taille d'un fichier déposé : relevé importé ou
	// pièce jointe.
	MaxUploadMB int
	// AttachmentsQuotaMB borne la place occupée par les pièces jointes.
	AttachmentsQuotaMB int
}

// Load construit la configuration à partir de getenv (os.Getenv en
// production). Toutes les erreurs sont signalées en une seule fois.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{Port: 8080, DataDir: "/data", LogLevel: slog.LevelInfo, MaxUploadMB: 10, AttachmentsQuotaMB: 1024}
	var errs []error

	for _, v := range []struct {
		name     string
		min, max int
		dst      *int
	}{
		{"PORT", 1, 65535, &cfg.Port},
		{"MAX_UPLOAD_MB", 1, 100, &cfg.MaxUploadMB},
		{"ATTACHMENTS_QUOTA_MB", 10, 1_000_000, &cfg.AttachmentsQuotaMB},
	} {
		raw := getenv(v.name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < v.min || n > v.max {
			errs = append(errs, fmt.Errorf("%s %q invalide : entier entre %d et %d attendu", v.name, raw, v.min, v.max))
			continue
		}
		*v.dst = n
	}
	if v := getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("LOG_LEVEL"); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL %q invalide : debug, info, warn ou error attendu", v))
		}
	}
	if v := getenv("BASE_URL"); v != "" {
		u, err := parseBaseURL(v)
		if err != nil {
			errs = append(errs, err)
		}
		cfg.BaseURL = u
	}
	if v := getenv("TRUSTED_PROXIES"); v != "" {
		p, err := parsePrefixes(v)
		if err != nil {
			errs = append(errs, err)
		}
		cfg.TrustedProxies = p
	}
	return cfg, errors.Join(errs...)
}

// Addr renvoie l'adresse d'écoute du serveur HTTP.
func (c Config) Addr() string { return ":" + strconv.Itoa(c.Port) }

// MaxUploadBytes renvoie la taille maximale d'un fichier déposé.
func (c Config) MaxUploadBytes() int64 { return int64(c.MaxUploadMB) << 20 }

// AttachmentsQuotaBytes renvoie la place allouée aux pièces jointes.
func (c Config) AttachmentsQuotaBytes() int64 { return int64(c.AttachmentsQuotaMB) << 20 }

// DBPath renvoie le chemin du fichier SQLite.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "buddyget.db") }

// SecureCookies indique si les cookies doivent porter l'attribut Secure,
// c'est-à-dire si l'application est publiée en HTTPS.
func (c Config) SecureCookies() bool { return c.BaseURL != nil && c.BaseURL.Scheme == "https" }

func parseBaseURL(v string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(v, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("BASE_URL %q invalide : URL http(s)://hôte attendue", v)
	}
	return u, nil
}

// parsePrefixes accepte une liste séparée par des virgules d'adresses IP
// ou de réseaux CIDR.
func parsePrefixes(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for item := range strings.SplitSeq(v, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES : %q n'est ni une adresse IP ni un réseau CIDR", item)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
