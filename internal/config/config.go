package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ConfigFileName      = "config.json"
	TokenFileName       = "state/access-token"
	TunnelTokenFileName = "state/tunnel-token"
	ConnectionFileName  = "state/connection.json"
	PIDFileName         = "state/service.json"
	LogFileName         = "logs/service.log"

	// TunnelModeQuick creates a temporary trycloudflare.com hostname.
	TunnelModeQuick = "quick"
	// TunnelModeNamed runs a Cloudflare tunnel token and serves a fixed hostname.
	TunnelModeNamed = "named"
)

type Config struct {
	Listen            string        `json:"listen"`
	KataGoPath        string        `json:"katagoPath"`
	ModelPath         string        `json:"modelPath"`
	HumanModelPath    string        `json:"humanModelPath,omitempty"`
	AnalysisConfig    string        `json:"analysisConfig"`
	CloudflaredPath   string        `json:"cloudflaredPath"`
	TunnelMode        string        `json:"tunnelMode,omitempty"`
	TunnelHostname    string        `json:"tunnelHostname,omitempty"`
	MaxMessageBytes   int64         `json:"maxMessageBytes"`
	MaxVisits         int64         `json:"maxVisits"`
	StartupTimeoutSec int           `json:"startupTimeoutSeconds"`
	TunnelTimeoutSec  int           `json:"tunnelTimeoutSeconds"`
	ExtraEnvironment  []string      `json:"extraEnvironment,omitempty"`
	StartupTimeout    time.Duration `json:"-"`
	TunnelTimeout     time.Duration `json:"-"`
}

func Default() Config {
	return Config{
		Listen:            "127.0.0.1:8765",
		TunnelMode:        TunnelModeQuick,
		MaxMessageBytes:   4 << 20,
		MaxVisits:         10_000_000,
		StartupTimeoutSec: 45,
		TunnelTimeoutSec:  45,
	}
}

func DefaultHome() (string, error) {
	if value := strings.TrimSpace(os.Getenv("KATAGO_REMOTE_HOME")); value != "" {
		return filepath.Abs(value)
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Abs(filepath.Join(dataHome, "katago-remote-one-click"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "katago-remote-one-click"), nil
}

func Load(home string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(home, ConfigFileName))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	cfg.StartupTimeout = time.Duration(cfg.StartupTimeoutSec) * time.Second
	cfg.TunnelTimeout = time.Duration(cfg.TunnelTimeoutSec) * time.Second
	return cfg, nil
}

func (cfg Config) Validate() error {
	if !strings.HasPrefix(cfg.Listen, "127.0.0.1:") && !strings.HasPrefix(cfg.Listen, "localhost:") {
		return errors.New("listen address must remain on localhost")
	}
	mode, err := NormalizeTunnelMode(cfg.TunnelMode)
	if err != nil {
		return err
	}
	hostname := strings.TrimSpace(cfg.TunnelHostname)
	switch mode {
	case TunnelModeQuick:
		if hostname != "" {
			return errors.New("tunnelHostname requires tunnelMode \"named\"")
		}
	case TunnelModeNamed:
		if err := ValidateTunnelHostname(hostname); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"katagoPath":      cfg.KataGoPath,
		"modelPath":       cfg.ModelPath,
		"analysisConfig":  cfg.AnalysisConfig,
		"cloudflaredPath": cfg.CloudflaredPath,
	} {
		if !filepath.IsAbs(value) {
			return fmt.Errorf("%s must be an absolute path", name)
		}
	}
	if cfg.HumanModelPath != "" && !filepath.IsAbs(cfg.HumanModelPath) {
		return errors.New("humanModelPath must be an absolute path")
	}
	if cfg.MaxMessageBytes < 1024 || cfg.MaxMessageBytes > 64<<20 {
		return errors.New("maxMessageBytes must be between 1 KiB and 64 MiB")
	}
	if cfg.MaxVisits < 1 || cfg.MaxVisits > 100_000_000 {
		return errors.New("maxVisits must be between 1 and 100000000")
	}
	if cfg.StartupTimeoutSec < 5 || cfg.StartupTimeoutSec > 300 {
		return errors.New("startupTimeoutSeconds must be between 5 and 300")
	}
	if cfg.TunnelTimeoutSec < 5 || cfg.TunnelTimeoutSec > 300 {
		return errors.New("tunnelTimeoutSeconds must be between 5 and 300")
	}
	for _, item := range cfg.ExtraEnvironment {
		if !strings.Contains(item, "=") || strings.HasPrefix(item, "=") {
			return errors.New("extraEnvironment entries must use NAME=value")
		}
	}
	return nil
}

// NormalizeTunnelMode returns the effective tunnel mode and rejects unknown values.
func NormalizeTunnelMode(value string) (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(value)); mode {
	case "", TunnelModeQuick:
		return TunnelModeQuick, nil
	case TunnelModeNamed:
		return TunnelModeNamed, nil
	default:
		return "", fmt.Errorf("tunnelMode must be %q or %q", TunnelModeQuick, TunnelModeNamed)
	}
}

// NamedTunnel reports whether the configuration serves a fixed Cloudflare hostname.
func (cfg Config) NamedTunnel() bool {
	mode, err := NormalizeTunnelMode(cfg.TunnelMode)
	return err == nil && mode == TunnelModeNamed
}

var tunnelHostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidateTunnelHostname checks a public hostname such as katago.example.org.
func ValidateTunnelHostname(value string) error {
	hostname := strings.TrimSpace(value)
	if hostname == "" {
		return errors.New("tunnelHostname is required for a named tunnel")
	}
	if hostname != value || hostname != strings.ToLower(hostname) {
		return fmt.Errorf("tunnelHostname %q must be lowercase without surrounding spaces", value)
	}
	if len(hostname) > 253 || !tunnelHostnamePattern.MatchString(hostname) {
		return fmt.Errorf("tunnelHostname %q is not a plain DNS name; use only the hostname without scheme, port, or path", value)
	}
	if strings.HasSuffix(hostname, ".trycloudflare.com") {
		return errors.New("tunnelHostname must belong to your own Cloudflare domain, not trycloudflare.com")
	}
	return nil
}

// MaxTunnelTokenBytes bounds the tunnel token accepted from flags, files, or environment.
const MaxTunnelTokenBytes = 8 << 10

// ValidateTunnelToken checks a Cloudflare Tunnel token before it is stored.
func ValidateTunnelToken(token string) error {
	if token == "" {
		return errors.New("tunnel token is empty")
	}
	if len(token) > MaxTunnelTokenBytes {
		return errors.New("tunnel token is too long")
	}
	if len(token) < 32 {
		return errors.New("tunnel token is too short")
	}
	for _, character := range token {
		if character <= ' ' || character > '~' {
			return errors.New("tunnel token contains whitespace or control characters")
		}
	}
	return nil
}

// SaveTunnelToken stores the Cloudflare Tunnel token with owner-only permissions.
func SaveTunnelToken(home, token string) error {
	if err := ValidateTunnelToken(token); err != nil {
		return err
	}
	if err := EnsurePrivateDirs(home); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(home, TunnelTokenFileName), []byte(token+"\n"), 0o600)
}

// ReadTunnelToken loads the stored Cloudflare Tunnel token.
func ReadTunnelToken(home string) (string, error) {
	data, err := os.ReadFile(filepath.Join(home, TunnelTokenFileName))
	if err != nil {
		return "", fmt.Errorf("read tunnel token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if err := ValidateTunnelToken(token); err != nil {
		return "", fmt.Errorf("tunnel token in %s: %w", filepath.Join(home, TunnelTokenFileName), err)
	}
	return token, nil
}

// HasTunnelToken reports whether a valid tunnel token file exists.
func HasTunnelToken(home string) bool {
	_, err := ReadTunnelToken(home)
	return err == nil
}

func Save(home string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := EnsurePrivateDirs(home); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(home, ConfigFileName), data, 0o600)
}

func EnsurePrivateDirs(home string) error {
	for _, path := range []string{home, filepath.Join(home, "state"), filepath.Join(home, "logs")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("secure %s: %w", path, err)
		}
	}
	return nil
}

func EnsureToken(home string, rotate bool) (string, error) {
	path := filepath.Join(home, TokenFileName)
	if !rotate {
		if data, err := os.ReadFile(path); err == nil {
			token := strings.TrimSpace(string(data))
			if len(token) >= 32 {
				return token, nil
			}
		}
	}
	if err := EnsurePrivateDirs(home); err != nil {
		return "", err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	if err := atomicWrite(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// SaveToken keeps the legacy access-token file in sync with the first link.
func SaveToken(home, token string) error {
	if len(token) < 32 {
		return errors.New("access token is too short")
	}
	if err := EnsurePrivateDirs(home); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(home, TokenFileName), []byte(token+"\n"), 0o600)
}

func ReadToken(home string) (string, error) {
	data, err := os.ReadFile(filepath.Join(home, TokenFileName))
	if err != nil {
		return "", fmt.Errorf("read access token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return "", errors.New("access token is missing or invalid; run init again")
	}
	return token, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
