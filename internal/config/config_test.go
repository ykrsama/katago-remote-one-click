package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTokenIsPrivateAndRotatesOnlyOnRequest(t *testing.T) {
	home := t.TempDir()
	first, err := EnsureToken(home, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EnsureToken(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatal("token changed without an explicit rotation")
	}
	rotated, err := EnsureToken(home, true)
	if err != nil {
		t.Fatal(err)
	}
	if rotated == first {
		t.Fatal("token did not rotate")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(home, TokenFileName))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("token mode = %o, want 600", got)
		}
	}
}

func TestConfigRejectsPublicListenAddress(t *testing.T) {
	cfg := Default()
	cfg.Listen = "0.0.0.0:8765"
	cfg.KataGoPath = "/tmp/katago"
	cfg.ModelPath = "/tmp/model"
	cfg.AnalysisConfig = "/tmp/config"
	cfg.CloudflaredPath = "/tmp/cloudflared"
	if err := cfg.Validate(); err == nil {
		t.Fatal("public listen address was accepted")
	}
}

func TestValidateTunnelHostname(t *testing.T) {
	for _, hostname := range []string{
		"katago.ykrsama.org",
		"a-b.example.com",
		"x.y.co",
	} {
		if err := ValidateTunnelHostname(hostname); err != nil {
			t.Fatalf("valid hostname %q rejected: %v", hostname, err)
		}
	}
	for _, hostname := range []string{
		"",
		"https://katago.ykrsama.org",
		"katago.ykrsama.org:443",
		"katago.ykrsama.org/katago/token",
		"Katago.ykrsama.org",
		" katago.ykrsama.org",
		"localhost",
		"katago",
		"bad_host.example.com",
		"quiet-sky.trycloudflare.com",
	} {
		if err := ValidateTunnelHostname(hostname); err == nil {
			t.Fatalf("invalid hostname %q accepted", hostname)
		}
	}
}

func TestNamedTunnelConfigValidation(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.KataGoPath = filepath.Join(dir, "katago")
	cfg.ModelPath = filepath.Join(dir, "model.bin.gz")
	cfg.AnalysisConfig = filepath.Join(dir, "analysis.cfg")
	cfg.CloudflaredPath = filepath.Join(dir, "cloudflared")

	cfg.TunnelMode = TunnelModeNamed
	if err := cfg.Validate(); err == nil {
		t.Fatal("named tunnel without a hostname was accepted")
	}
	cfg.TunnelHostname = "katago.ykrsama.org"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("named tunnel rejected: %v", err)
	}
	cfg.TunnelMode = TunnelModeQuick
	if err := cfg.Validate(); err == nil {
		t.Fatal("quick tunnel with a hostname was accepted")
	}
	cfg.TunnelHostname = ""
	cfg.TunnelMode = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown tunnel mode was accepted")
	}
}

func TestTunnelTokenIsPrivateAndValidated(t *testing.T) {
	home := t.TempDir()
	token := strings.Repeat("a", 64)
	if err := SaveTunnelToken(home, token); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTunnelToken(home)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token = %q", got)
	}
	if !HasTunnelToken(home) {
		t.Fatal("stored token was not detected")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(home, TunnelTokenFileName))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("tunnel token mode = %o, want 600", got)
		}
	}
	for _, invalid := range []string{"", "short", strings.Repeat("a", 64) + " ", strings.Repeat("a", MaxTunnelTokenBytes+1)} {
		if err := ValidateTunnelToken(invalid); err == nil {
			t.Fatalf("invalid token of length %d accepted", len(invalid))
		}
	}
}
