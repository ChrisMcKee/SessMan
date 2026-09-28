package secretstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOIDCClientRoundTripEncrypted(t *testing.T) {
	s, err := NewAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	secret := strings.Repeat("a", 4000)
	in := OIDCClient{
		ClientID:     "client",
		ClientSecret: secret,
		ExpiresAt:    123,
	}

	if err := s.SaveOIDCClient("integ-1", in); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(s.path("oidc", "integ-1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext secret found on disk")
	}
	if !strings.HasPrefix(string(raw), fileMagic) {
		t.Fatalf("missing magic header: %q", raw[:min(8, len(raw))])
	}

	out, err := s.GetOIDCClient("integ-1")
	if err != nil {
		t.Fatal(err)
	}
	if out.ClientID != in.ClientID || out.ExpiresAt != in.ExpiresAt || out.ClientSecret != in.ClientSecret {
		t.Fatalf("mismatch: %+v", out)
	}

	if _, err := s.GetToken("integ-1"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}

	_ = s.DeleteIntegrationSecrets("integ-1")
	if _, err := s.GetOIDCClient("integ-1"); !IsNotFound(err) {
		t.Fatalf("expected deleted, got %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(s.dir, "*"))
	if len(matches) != 0 {
		t.Fatalf("expected no files, got %v", matches)
	}
}

func TestLegacyPlaintextMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := NewAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(dir, "oidc-legacy.json")
	plain := `{"clientId":"c1","clientSecret":"s1","expiresAt":99}`
	if err := os.WriteFile(legacy, []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := s.GetOIDCClient("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if out.ClientID != "c1" || out.ClientSecret != "s1" {
		t.Fatalf("unexpected: %+v", out)
	}

	// Migrated encrypted file should exist; legacy should be gone after read.
	if _, err := os.Stat(s.path("oidc", "legacy")); err != nil {
		t.Fatalf("expected encrypted file: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy plaintext file should be removed after migration")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(i)
	}
	plain := []byte(`{"hello":"world"}`)
	sealed, err := seal(key, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := open(key, sealed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("got %s", got)
	}

	wrong := make([]byte, keySize)
	for i := range wrong {
		wrong[i] = byte(255 - i)
	}
	if _, err := open(wrong, sealed, nil); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}

func TestUnreadableSecretIsNotFound(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SaveToken("int1", Token{AccessToken: "x", ExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}
	// A store with a different master key (e.g. keyring entry was lost).
	b, _ := NewAt(dir)
	if _, err := b.GetToken("int1"); !IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := b.GetToken("int1"); !IsNotFound(err) {
		t.Fatalf("file should have been discarded: %v", err)
	}
}

func TestSecretBoundToIntegration(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewAt(dir)
	if err := s.SaveToken("a", Token{AccessToken: "secret-a", ExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.path("token", "a"))
	if err := os.WriteFile(s.path("token", "b"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetToken("b"); !IsNotFound(err) {
		t.Fatalf("swapped file must not decrypt: %v", err)
	}
}
