package sessionmgr

import (
	"path/filepath"
	"testing"

	"awssession/internal/domain"
	"awssession/internal/workspace"
)

func TestDefaultProfileName(t *testing.T) {
	got := defaultProfileName("My Account", "AdministratorAccess")
	if got != "my-account-administratoraccess" {
		t.Fatalf("got %q", got)
	}
}

func TestUniqueProfile(t *testing.T) {
	used := map[string]bool{"dev-admin": true}
	got := uniqueProfile("dev-admin", used)
	if got != "dev-admin-2" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeProfile(t *testing.T) {
	if sanitizeProfile("  foo/bar baz  ") != "foo-bar-baz" {
		t.Fatal(sanitizeProfile("  foo/bar baz  "))
	}
}

func TestListSessionsPinnedFirst(t *testing.T) {
	dir := t.TempDir()
	store, err := workspace.NewStoreAt(filepath.Join(dir, "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := New(store, nil, nil)
	_ = store.Update(func(ws *domain.Workspace) error {
		ws.Sessions = []domain.Session{
			{ID: "1", AccountName: "zeta", RoleName: "r", ProfileName: "a", State: domain.SessionInactive},
			{ID: "2", AccountName: "alpha", RoleName: "r", ProfileName: "b", State: domain.SessionInactive, Pinned: true},
			{ID: "3", AccountName: "beta", RoleName: "r", ProfileName: "c", State: domain.SessionInactive},
		}
		return nil
	})
	list := mgr.ListSessions()
	if list[0].ID != "2" {
		t.Fatalf("pinned should be first: %+v", list)
	}
}
