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
}

// Load construit la configuration à partir de getenv (os.Getenv en
// production). Toutes les erreurs sont signalées en une seule fois.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{Port: 8080, DataDir: "/data", LogLevel: slog.LevelInfo}
	var errs []error

	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			errs = append(errs, fmt.Errorf("PORT %q invalide : entier entre 1 et 65535 attendu", v))
		} else {
			cfg.Port = p
		}
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
