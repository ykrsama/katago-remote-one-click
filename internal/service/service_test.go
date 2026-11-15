package service

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/ykrsama/katago-remote-one-click/internal/config"
)

func TestBuildConnectionURL(t *testing.T) {
	got, err := buildConnectionURL("https://quiet-sky.trycloudflare.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://quiet-sky.trycloudflare.com/katago/secret" {
		t.Fatalf("URL = %q", got)
	}
}

func TestPrintAndWriteQR(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := PrintQR(&output, "wss://example.test/katago/secret"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) == "" {
		t.Fatal("terminal QR code is empty")
	}
	path, err := WriteQR(home, "wss://example.test/katago/secret")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("PNG QR code is empty")
	}
}

func TestInitializeKeepsExistingSettingsWhenOnlyTunnelChanges(t *testing.T) {
	home := t.TempDir()
	files := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"katago", "model.bin.gz", "analysis.cfg", "cloudflared"} {
		path := filepath.Join(files, name)
		if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	if err := Initialize(home, InitOptions{
		KataGo:      paths["katago"],
		Model:       paths["model.bin.gz"],
		Config:      paths["analysis.cfg"],
		Cloudflared: paths["cloudflared"],
		MaxVisits:   4242,
	}); err != nil {
		t.Fatal(err)
	}

	token := strings.Repeat("T", 64)
	if err := Initialize(home, InitOptions{
		TunnelMode:     appconfig.TunnelModeNamed,
		TunnelHostname: "katago.ykrsama.org",
		TunnelToken:    token,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := appconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KataGoPath != paths["katago"] || cfg.ModelPath != paths["model.bin.gz"] {
		t.Fatalf("paths were reset: %+v", cfg)
	}
	if cfg.MaxVisits != 4242 {
		t.Fatalf("maxVisits = %d", cfg.MaxVisits)
	}
	if !cfg.NamedTunnel() || cfg.TunnelHostname != "katago.ykrsama.org" {
		t.Fatalf("tunnel config = %+v", cfg)
	}
	stored, err := appconfig.ReadTunnelToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if stored != token {
		t.Fatal("tunnel token was not stored")
	}
}

func TestInitializeRejectsNamedTunnelWithoutToken(t *testing.T) {
	home := t.TempDir()
	files := t.TempDir()
	paths := map[string]string{}
	for _, name := range []string{"katago", "model.bin.gz", "analysis.cfg", "cloudflared"} {
		path := filepath.Join(files, name)
		if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	err := Initialize(home, InitOptions{
		KataGo:         paths["katago"],
		Model:          paths["model.bin.gz"],
		Config:         paths["analysis.cfg"],
		Cloudflared:    paths["cloudflared"],
		TunnelMode:     appconfig.TunnelModeNamed,
		TunnelHostname: "katago.ykrsama.org",
	})
	if err == nil {
		t.Fatal("named tunnel without a token was accepted")
	}
}
