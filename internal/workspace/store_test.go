package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"awssession/internal/domain"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}

	err = s.Update(func(ws *domain.Workspace) error {
		ws.Integrations = append(ws.Integrations, domain.Integration{
			ID:        "1",
			Name:      "prod",
			StartURL:  "https://example.awsapps.com/start",
			SSORegion: "eu-west-1",
			Status:    domain.IntegrationLoggedOut,
		})
		ws.Settings.DefaultRegion = "us-east-1"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	ws := s2.Get()
	if len(ws.Integrations) != 1 || ws.Integrations[0].Name != "prod" {
		t.Fatalf("unexpected integrations: %+v", ws.Integrations)
	}
	if ws.Settings.DefaultRegion != "us-east-1" {
		t.Fatalf("settings not persisted: %+v", ws.Settings)
	}
}

func TestCorruptWorkspaceIsSetAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.RecoveredFrom() == "" {
		t.Fatal("expected backup path")
	}
	if _, err := os.Stat(s.RecoveredFrom()); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if len(s.Get().Integrations) != 0 {
		t.Fatal("expected fresh workspace")
	}
}

func TestUpdateRollsBackOnError(t *testing.T) {
	s, _ := NewStoreAt(filepath.Join(t.TempDir(), "workspace.json"))
	err := s.Update(func(ws *domain.Workspace) error {
		ws.Settings.DefaultRegion = "mutated"
		ws.Sessions = append(ws.Sessions, domain.Session{ID: "x"})
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error")
	}
	got := s.Get()
	if got.Settings.DefaultRegion == "mutated" || len(got.Sessions) != 0 {
		t.Fatalf("state not rolled back: %+v", got)
	}
}
