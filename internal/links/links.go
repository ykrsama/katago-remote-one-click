package links

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// FileName is the private link registry inside the installation home.
	FileName = "state/links.json"
	// Unlimited means a link accepts any number of concurrent clients.
	Unlimited = -1
	// Disabled keeps a link in the registry but refuses new connections.
	Disabled = 0
)

// Link is one private WebSocket endpoint. The token is the secret path segment.
type Link struct {
	ID         string    `json:"id"`
	Token      string    `json:"token"`
	MaxClients int       `json:"maxClients"`
	CreatedAt  time.Time `json:"createdAt"`
}

type fileFormat struct {
	Version int    `json:"version"`
	Links   []Link `json:"links"`
}

// Store reads and writes the link registry under an installation home.
type Store struct {
	home string
}

func NewStore(home string) *Store {
	return &Store{home: home}
}

func (s *Store) Path() string {
	return filepath.Join(s.home, FileName)
}

// Load reads the registry. A missing file returns the os.ErrNotExist error.
func (s *Store) Load() ([]Link, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return nil, err
	}
	var parsed fileFormat
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse links: %w", err)
	}
	seenIDs := make(map[string]bool, len(parsed.Links))
	seenTokens := make(map[string]bool, len(parsed.Links))
	result := make([]Link, 0, len(parsed.Links))
	for _, link := range parsed.Links {
		if err := validateLink(link); err != nil {
			return nil, err
		}
		if seenIDs[link.ID] {
			return nil, fmt.Errorf("duplicate link id %q", link.ID)
		}
		if seenTokens[link.Token] {
			return nil, fmt.Errorf("duplicate link token for %q", link.ID)
		}
		seenIDs[link.ID] = true
		seenTokens[link.Token] = true
		result = append(result, link)
	}
	return result, nil
}

// LoadOrCreate migrates a legacy access token into the registry on first use.
func (s *Store) LoadOrCreate(legacyToken string) ([]Link, error) {
	current, err := s.Load()
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	token := strings.TrimSpace(legacyToken)
	if token == "" {
		token, err = GenerateToken()
		if err != nil {
			return nil, err
		}
	}
	link, err := newLink(token, Unlimited)
	if err != nil {
		return nil, err
	}
	if err := s.save([]Link{link}); err != nil {
		return nil, err
	}
	return []Link{link}, nil
}

// Create adds a new link with the given client limit.
func (s *Store) Create(maxClients int) (Link, error) {
	if err := ValidateMaxClients(maxClients); err != nil {
		return Link{}, err
	}
	current, err := s.Load()
	if err != nil {
		return Link{}, err
	}
	token, err := GenerateToken()
	if err != nil {
		return Link{}, err
	}
	link, err := newLink(token, maxClients)
	if err != nil {
		return Link{}, err
	}
	for idTaken(current, link.ID) {
		if link.ID, err = newID(); err != nil {
			return Link{}, err
		}
	}
	return link, s.save(append(current, link))
}

// SetMaxClients updates one link's client limit.
func (s *Store) SetMaxClients(ref string, maxClients int) (Link, error) {
	if err := ValidateMaxClients(maxClients); err != nil {
		return Link{}, err
	}
	current, err := s.Load()
	if err != nil {
		return Link{}, err
	}
	target, err := Resolve(current, ref)
	if err != nil {
		return Link{}, err
	}
	for i := range current {
		if current[i].ID == target.ID {
			current[i].MaxClients = maxClients
			target = current[i]
		}
	}
	return target, s.save(current)
}

// Delete removes a link from the registry.
func (s *Store) Delete(ref string) (Link, error) {
	current, err := s.Load()
	if err != nil {
		return Link{}, err
	}
	target, err := Resolve(current, ref)
	if err != nil {
		return Link{}, err
	}
	remaining := make([]Link, 0, len(current))
	for _, link := range current {
		if link.ID != target.ID {
			remaining = append(remaining, link)
		}
	}
	return target, s.save(remaining)
}

// RotateToken replaces a link token. An empty reference rotates the first link.
func (s *Store) RotateToken(ref string) (Link, error) {
	current, err := s.Load()
	if err != nil {
		return Link{}, err
	}
	var target Link
	if strings.TrimSpace(ref) == "" {
		if len(current) == 0 {
			return Link{}, errors.New("no links exist; run katago-remote link create")
		}
		target = current[0]
	} else if target, err = Resolve(current, ref); err != nil {
		return Link{}, err
	}
	token, err := GenerateToken()
	if err != nil {
		return Link{}, err
	}
	for i := range current {
		if current[i].ID == target.ID {
			current[i].Token = token
			target = current[i]
		}
	}
	return target, s.save(current)
}

// FindByToken looks up a link without leaking timing information.
func FindByToken(list []Link, token string) (Link, bool) {
	for _, link := range list {
		if len(link.Token) == len(token) &&
			subtle.ConstantTimeCompare([]byte(link.Token), []byte(token)) == 1 {
			return link, true
		}
	}
	return Link{}, false
}

// Resolve accepts an exact token, an exact link id, or an unambiguous id prefix.
func Resolve(list []Link, ref string) (Link, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Link{}, errors.New("link id is required")
	}
	for _, link := range list {
		if link.Token == ref {
			return link, nil
		}
	}
	matches := make([]Link, 0, 1)
	for _, link := range list {
		if link.ID == ref || strings.HasPrefix(link.ID, ref) {
			matches = append(matches, link)
		}
	}
	switch len(matches) {
	case 0:
		return Link{}, fmt.Errorf("no link matches %q", ref)
	case 1:
		return matches[0], nil
	default:
		return Link{}, fmt.Errorf("%q matches multiple links; use more characters", ref)
	}
}

// DescribeMax renders a client limit for command output.
func DescribeMax(maxClients int) string {
	switch maxClients {
	case Unlimited:
		return "unlimited"
	case Disabled:
		return "0 (disabled)"
	default:
		return strconv.Itoa(maxClients)
	}
}

// GenerateToken returns a 256-bit URL-safe token.
func GenerateToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// ValidateToken checks a link token before it is stored or compared.
func ValidateToken(token string) error {
	if len(token) < 32 {
		return errors.New("link token is too short")
	}
	if len(token) > 256 {
		return errors.New("link token is too long")
	}
	for _, character := range token {
		if character <= ' ' || character > '~' {
			return errors.New("link token contains whitespace or control characters")
		}
	}
	return nil
}

// ValidateMaxClients accepts -1 (unlimited), 0 (disabled), or a positive cap.
func ValidateMaxClients(maxClients int) error {
	if maxClients < Unlimited {
		return errors.New("maxClients must be -1 (unlimited), 0 (disabled), or a positive number")
	}
	return nil
}

func validateLink(link Link) error {
	if strings.TrimSpace(link.ID) == "" {
		return errors.New("link id is missing")
	}
	if err := ValidateToken(link.Token); err != nil {
		return fmt.Errorf("link %q: %w", link.ID, err)
	}
	if err := ValidateMaxClients(link.MaxClients); err != nil {
		return fmt.Errorf("link %q: %w", link.ID, err)
	}
	return nil
}

func newLink(token string, maxClients int) (Link, error) {
	id, err := newID()
	if err != nil {
		return Link{}, err
	}
	return Link{
		ID:         id,
		Token:      token,
		MaxClients: maxClients,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

func newID() (string, error) {
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate link id: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func idTaken(list []Link, id string) bool {
	for _, link := range list {
		if link.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) save(list []Link) error {
	if err := ensureDirs(s.home); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(fileFormat{Version: 1, Links: list}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode links: %w", err)
	}
	payload = append(payload, '\n')
	path := s.Path()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secure %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func ensureDirs(home string) error {
	for _, dir := range []string{home, filepath.Join(home, "state")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure %s: %w", dir, err)
		}
	}
	return nil
}
