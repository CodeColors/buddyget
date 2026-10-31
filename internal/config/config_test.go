package config

import (
	"log/slog"
	"net/netip"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != ":8080" || cfg.DBPath() != "/data/buddyget.db" || cfg.LogLevel != slog.LevelInfo || cfg.MaxUploadBytes() != 10<<20 {
		t.Errorf("valeurs par défaut inattendues : %+v", cfg)
	}
	if cfg.SecureCookies() || cfg.BaseURL != nil || cfg.TrustedProxies != nil {
		t.Errorf("aucune option réseau ne doit être active par défaut : %+v", cfg)
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"PORT":                 "9000",
		"DATA_DIR":             "/srv/bg",
		"LOG_LEVEL":            "debug",
		"BASE_URL":             "https://budget.example.fr/",
		"TRUSTED_PROXIES":      "172.16.0.0/12, 192.168.1.10",
		"MAX_UPLOAD_MB":        "25",
		"ATTACHMENTS_QUOTA_MB": "500",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != ":9000" || cfg.DBPath() != "/srv/bg/buddyget.db" || cfg.LogLevel != slog.LevelDebug || cfg.MaxUploadMB != 25 ||
		cfg.AttachmentsQuotaBytes() != 500<<20 {
		t.Errorf("valeurs inattendues : %+v", cfg)
	}
	if !cfg.SecureCookies() || cfg.BaseURL.String() != "https://budget.example.fr" {
		t.Errorf("BASE_URL mal interprétée : %v", cfg.BaseURL)
	}
	want := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.1.10/32")}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("TRUSTED_PROXIES = %v, attendu %v", cfg.TrustedProxies, want)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Errorf("TRUSTED_PROXIES[%d] = %v, attendu %v", i, cfg.TrustedProxies[i], want[i])
		}
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"port non numérique": {"PORT": "abc"},
		"port hors plage":    {"PORT": "70000"},
		"niveau inconnu":     {"LOG_LEVEL": "bavard"},
		"URL sans schéma":    {"BASE_URL": "budget.example.fr"},
		"proxy invalide":     {"TRUSTED_PROXIES": "10.0.0.0/8,proxy"},
		"taille d'import":    {"MAX_UPLOAD_MB": "0"},
		"quota des pièces":   {"ATTACHMENTS_QUOTA_MB": "5"},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(vars)); err == nil {
				t.Error("erreur attendue")
			}
		})
	}
}
