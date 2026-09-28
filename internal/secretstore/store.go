package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when a secret does not exist.
var ErrNotFound = errors.New("secret not found")

const (
	appDirName     = "sessman"
	secretsDirName = "secrets"
	keyringService = "sessman"
	keyringUser    = "master-key"
	fileMagic      = "AWSSSEC1" // 8 bytes
	keySize        = 32
)

// OIDCClient holds registered OIDC client credentials for an integration.
type OIDCClient struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	ExpiresAt    int64  `json:"expiresAt"`
}

// Token holds the SSO access token and optional refresh token.
// RefreshToken is used to renew AccessToken while the Identity Center
// portal session is still alive (see IAM Identity Center OIDC CreateToken).
type Token struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt"` // unix seconds
}

// Store encrypts secrets on disk with a DEK kept in the OS credential store
// (Windows Credential Manager, macOS Keychain, or Linux Secret Service via
// github.com/zalando/go-keyring). Large OIDC payloads exceed Windows Cred
// Manager limits, so only the small master key lives there; ciphertext is
// stored under the OS app config dir.
type Store struct {
	mu  sync.Mutex
	dir string
	key []byte
}

// New creates a secret store under the app config directory.
func New() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("user config dir: %w", err)
	}
	dir := filepath.Join(base, appDirName, secretsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}
	key, err := loadOrCreateMasterKey()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, key: key}, nil
}

// NewAt is useful for tests (in-memory master key, no OS keyring).
func NewAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate test key: %w", err)
	}
	return &Store{dir: dir, key: key}, nil
}

func loadOrCreateMasterKey() ([]byte, error) {
	raw, err := keyring.Get(keyringService, keyringUser)
	if err == nil {
		key, decErr := base64.StdEncoding.DecodeString(raw)
		if decErr != nil {
			return nil, fmt.Errorf("decode master key: %w", decErr)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("invalid master key length %d", len(key))
		}
		return key, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("read master key from credential store: %w", err)
	}

	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := keyring.Set(keyringService, keyringUser, encoded); err != nil {
		return nil, fmt.Errorf("store master key in credential store: %w", err)
	}
	return key, nil
}

func (s *Store) path(kind, integrationID string) string {
	safe := sanitizeID(integrationID)
	return filepath.Join(s.dir, fmt.Sprintf("%s-%s.enc", kind, safe))
}

func (s *Store) legacyPath(kind, integrationID string) string {
	safe := sanitizeID(integrationID)
	return filepath.Join(s.dir, fmt.Sprintf("%s-%s.json", kind, safe))
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unknown"
	}
	return out
}

// SaveOIDCClient stores OIDC client registration.
func (s *Store) SaveOIDCClient(integrationID string, c OIDCClient) error {
	return s.setJSON("oidc", integrationID, c)
}

// GetOIDCClient loads OIDC client registration.
func (s *Store) GetOIDCClient(integrationID string) (OIDCClient, error) {
	var c OIDCClient
	err := s.getJSON("oidc", integrationID, &c)
	return c, err
}

// SaveToken stores the SSO access token.
func (s *Store) SaveToken(integrationID string, t Token) error {
	return s.setJSON("token", integrationID, t)
}

// GetToken loads the SSO access token.
func (s *Store) GetToken(integrationID string) (Token, error) {
	var t Token
	err := s.getJSON("token", integrationID, &t)
	return t, err
}

// DeleteIntegrationSecrets removes OIDC + token for an integration.
func (s *Store) DeleteIntegrationSecrets(integrationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.path("oidc", integrationID))
	_ = os.Remove(s.path("token", integrationID))
	_ = os.Remove(s.legacyPath("oidc", integrationID))
	_ = os.Remove(s.legacyPath("token", integrationID))
	return nil
}

func (s *Store) setJSON(kind, integrationID string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sealed, err := seal(s.key, data, aad(kind, integrationID))
	if err != nil {
		return err
	}
	path := s.path(kind, integrationID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename secret: %w", err)
	}
	// Drop any leftover plaintext file from older versions.
	_ = os.Remove(s.legacyPath(kind, integrationID))
	return nil
}

func (s *Store) getJSON(kind, integrationID string, dest any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.path(kind, integrationID)
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// Migrate legacy plaintext JSON if present.
		legacy := s.legacyPath(kind, integrationID)
		legacyRaw, lerr := os.ReadFile(legacy)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				return ErrNotFound
			}
			return lerr
		}
		if err := json.Unmarshal(legacyRaw, dest); err != nil {
			return err
		}
		// Best-effort rewrite as encrypted; ignore failure so reads still work.
		if sealed, serr := seal(s.key, legacyRaw, aad(kind, integrationID)); serr == nil {
			_ = os.WriteFile(path, sealed, 0o600)
			_ = os.Remove(legacy)
		}
		return nil
	}

	plain, err := open(s.key, raw, aad(kind, integrationID))
	if err == nil {
		err = json.Unmarshal(plain, dest)
	}
	if err != nil {
		// Unreadable (master key lost/rotated, file swapped or corrupt): the
		// data is useless, so drop it and let the caller treat it as "not
		// logged in" instead of failing forever with an opaque error.
		_ = os.Remove(path)
		return fmt.Errorf("%w (discarded unreadable %s secret: %v)", ErrNotFound, kind, err)
	}
	return nil
}

// aad binds a ciphertext to its kind and integration so files cannot be swapped.
func aad(kind, integrationID string) []byte {
	return []byte(kind + "/" + sanitizeID(integrationID))
}

func seal(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	out := make([]byte, 0, len(fileMagic)+len(nonce)+len(ciphertext))
	out = append(out, fileMagic...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

func open(key, blob, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(blob) < len(fileMagic)+nonceSize {
		return nil, fmt.Errorf("secret blob too short")
	}
	if string(blob[:len(fileMagic)]) != fileMagic {
		return nil, fmt.Errorf("unrecognized secret file format")
	}
	nonce := blob[len(fileMagic) : len(fileMagic)+nonceSize]
	ciphertext := blob[len(fileMagic)+nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return plain, nil
}

// IsNotFound reports missing secrets.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
