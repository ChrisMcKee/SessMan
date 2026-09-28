package sessionmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"awssession/internal/awsfiles"
	"awssession/internal/domain"
	"awssession/internal/sso"
	"awssession/internal/workspace"
)

type fakeSSO struct {
	mu      sync.Mutex
	credErr error
	creds   sso.RoleCredentials
	calls   int
	gate    chan struct{} // if set, GetRoleCredentials blocks until closed
	login   func(ctx context.Context) error
	roles   []sso.AccountRole
}

func (f *fakeSSO) Login(ctx context.Context, _, _, _ string, _ sso.DeviceAuthCallback) error {
	if f.login != nil {
		return f.login(ctx)
	}
	return nil
}

func (f *fakeSSO) ListAccountRoles(context.Context, string, string) ([]sso.AccountRole, error) {
	return f.roles, nil
}

func (f *fakeSSO) GetRoleCredentials(context.Context, string, string, string, string) (sso.RoleCredentials, error) {
	f.mu.Lock()
	f.calls++
	gate, err, creds := f.gate, f.credErr, f.creds
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return creds, err
}

func (f *fakeSSO) Logout(string) error { return nil }

func (f *fakeSSO) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type env struct {
	mgr      *Manager
	store    *workspace.Store
	credPath string
}

func newEnv(t *testing.T, f *fakeSSO, sess domain.Session) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := workspace.NewStoreAt(filepath.Join(dir, "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(dir, "credentials")
	w := awsfiles.NewWriterAt(credPath, filepath.Join(dir, "config"))
	sess.ID, sess.IntegrationID, sess.ProfileName = "s1", "i1", "dev"
	sess.AccountID, sess.RoleName = "111", "Admin"
	_ = store.Update(func(ws *domain.Workspace) error {
		ws.Integrations = []domain.Integration{{ID: "i1", Name: "n", StartURL: "https://x.awsapps.com/start", SSORegion: "eu-west-1", Status: domain.IntegrationLoggedIn}}
		ws.Sessions = []domain.Session{sess}
		return nil
	})
	return &env{mgr: New(store, w, f), store: store, credPath: credPath}
}

func (e *env) session(t *testing.T) domain.Session {
	t.Helper()
	s, err := e.mgr.GetSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func soon(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func TestStartSessionWritesProfile(t *testing.T) {
	f := &fakeSSO{creds: sso.RoleCredentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST", Expiration: time.Now().Add(time.Hour)}}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive})
	if err := e.mgr.StartSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if s := e.session(t); s.State != domain.SessionActive || s.ExpiresAt == "" {
		t.Fatalf("bad session: %+v", s)
	}
	data, _ := os.ReadFile(e.credPath)
	if !strings.Contains(string(data), "[dev]") || !strings.Contains(string(data), "AK") {
		t.Fatalf("profile not written: %s", data)
	}
}

func TestRotationFailureKeepsSessionActiveAndRetries(t *testing.T) {
	f := &fakeSSO{credErr: errors.New("network down")}
	exp := soon(time.Minute)
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: exp})

	if err := e.mgr.RotateActive(context.Background()); err == nil {
		t.Fatal("want rotation error")
	}
	s := e.session(t)
	if s.State != domain.SessionActive || s.ExpiresAt != exp || s.LastError == "" {
		t.Fatalf("session must stay Active with its expiry after a transient failure: %+v", s)
	}

	// Next tick succeeds and the session recovers.
	f.mu.Lock()
	f.credErr = nil
	f.creds = sso.RoleCredentials{AccessKeyID: "NEW", SecretAccessKey: "SK", SessionToken: "ST", Expiration: time.Now().Add(time.Hour)}
	f.mu.Unlock()
	if err := e.mgr.RotateActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	s = e.session(t)
	if s.State != domain.SessionActive || s.LastError != "" || s.ExpiresAt == exp {
		t.Fatalf("session not refreshed: %+v", s)
	}
	if data, _ := os.ReadFile(e.credPath); !strings.Contains(string(data), "NEW") {
		t.Fatalf("credentials not rewritten: %s", data)
	}
}

func TestRotationFailureAfterExpiryMarksError(t *testing.T) {
	f := &fakeSSO{credErr: errors.New("network down")}
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: soon(-time.Minute)})
	_ = e.mgr.RotateActive(context.Background())
	if s := e.session(t); s.State != domain.SessionError {
		t.Fatalf("lapsed credentials should be Error, got %+v", s)
	}
}

func TestRotationSkipsFreshCredentials(t *testing.T) {
	f := &fakeSSO{}
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: soon(3 * time.Hour)})
	if err := e.mgr.RotateActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.callCount() != 0 {
		t.Fatal("fresh credentials should not be refreshed")
	}
}

func TestAuthErrorMarksIntegrationExpiredAndNotifies(t *testing.T) {
	f := &fakeSSO{credErr: fmt.Errorf("get role credentials: %w", sso.ErrTokenExpired)}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive})
	var got string
	e.mgr.SetOnAuthRequired(func(id string) { got = id })
	if err := e.mgr.StartSession(context.Background(), "s1"); err == nil {
		t.Fatal("want error")
	}
	if got != "i1" {
		t.Fatalf("auth callback not fired: %q", got)
	}
	if e.mgr.ListIntegrations()[0].Status != domain.IntegrationExpired {
		t.Fatal("integration not marked expired")
	}
	if s := e.session(t); s.State != domain.SessionError {
		t.Fatalf("want Error, got %+v", s)
	}
}

func TestEnsureActiveRefreshesLapsedActiveSession(t *testing.T) {
	f := &fakeSSO{creds: sso.RoleCredentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST", Expiration: time.Now().Add(time.Hour)}}
	oldExp := soon(-time.Minute)
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: oldExp})
	sess, err := e.mgr.EnsureActive(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if f.callCount() != 1 {
		t.Fatalf("lapsed Active session should be refreshed, got %d fetches", f.callCount())
	}
	if sess.State != domain.SessionActive || sess.ExpiresAt == "" || sess.ExpiresAt == oldExp {
		t.Fatalf("session not refreshed: %+v", sess)
	}
}

func TestEnsureActiveSkipsFreshActiveSession(t *testing.T) {
	f := &fakeSSO{}
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: soon(time.Hour)})
	if _, err := e.mgr.EnsureActive(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if f.callCount() != 0 {
		t.Fatal("fresh Active session should not be refreshed")
	}
}

func TestEnsureActiveAuthErrorMarksIntegrationExpired(t *testing.T) {
	f := &fakeSSO{credErr: fmt.Errorf("get role credentials: %w", sso.ErrTokenExpired)}
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: soon(-time.Minute)})
	var got string
	e.mgr.SetOnAuthRequired(func(id string) { got = id })
	if _, err := e.mgr.EnsureActive(context.Background(), "s1"); err == nil {
		t.Fatal("want error")
	}
	if got != "i1" {
		t.Fatalf("auth callback not fired: %q", got)
	}
	if e.mgr.ListIntegrations()[0].Status != domain.IntegrationExpired {
		t.Fatal("integration should show Expired, not LoggedIn")
	}
}

func TestStartRotationRefreshesImmediately(t *testing.T) {
	f := &fakeSSO{creds: sso.RoleCredentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST", Expiration: time.Now().Add(time.Hour)}}
	e := newEnv(t, f, domain.Session{State: domain.SessionActive, ExpiresAt: soon(-time.Minute)})
	_ = e.store.Update(func(ws *domain.Workspace) error {
		ws.Settings.RotationIntervalMin = 60
		return nil
	})
	e.mgr.StartRotation()
	defer e.mgr.StopRotation()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.callCount() >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("StartRotation should refresh lapsed Active sessions immediately")
}

func TestConcurrentStartsShareOneFetch(t *testing.T) {
	f := &fakeSSO{gate: make(chan struct{}), creds: sso.RoleCredentials{AccessKeyID: "AK", Expiration: time.Now().Add(time.Hour)}}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := func() {
		defer wg.Done()
		errs <- e.mgr.StartSession(context.Background(), "s1")
	}
	wg.Add(1)
	go start()
	for f.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	wg.Add(1)
	go start()
	time.Sleep(50 * time.Millisecond)
	close(f.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.callCount() != 1 {
		t.Fatalf("expected a single credential fetch, got %d", f.callCount())
	}
}

func TestStopDuringStartIsNotResurrected(t *testing.T) {
	f := &fakeSSO{gate: make(chan struct{}), creds: sso.RoleCredentials{AccessKeyID: "AK", Expiration: time.Now().Add(time.Hour)}}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive})
	done := make(chan error, 1)
	go func() { done <- e.mgr.StartSession(context.Background(), "s1") }()
	for f.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	if err := e.mgr.StopSession("s1"); err != nil {
		t.Fatal(err)
	}
	close(f.gate)
	if err := <-done; err == nil {
		t.Fatal("interrupted start should report an error")
	}
	if s := e.session(t); s.State != domain.SessionInactive {
		t.Fatalf("stopped session was reactivated: %+v", s)
	}
	if data, _ := os.ReadFile(e.credPath); strings.Contains(string(data), "[dev]") {
		t.Fatalf("credentials written after stop: %s", data)
	}
}

func TestLoginCancelAndSingleLogin(t *testing.T) {
	started := make(chan struct{})
	f := &fakeSSO{login: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive})
	done := make(chan error, 1)
	go func() { done <- e.mgr.Login(context.Background(), "i1") }()
	<-started

	if err := e.mgr.Login(context.Background(), "i1"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("second login should be rejected, got %v", err)
	}
	e.mgr.CancelLogin("i1")
	if err := <-done; err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("want cancelled, got %v", err)
	}
	// A new login may start again afterwards.
	f.login = nil
	if err := e.mgr.Login(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertSessionsKeepsStateAndCleansVanishedRoles(t *testing.T) {
	f := &fakeSSO{roles: []sso.AccountRole{{AccountID: "111", AccountName: "acct", RoleName: "Admin"}}}
	e := newEnv(t, f, domain.Session{State: domain.SessionInactive, Pinned: true})

	// A second, active session whose role will vanish.
	_ = e.store.Update(func(ws *domain.Workspace) error {
		ws.Sessions = append(ws.Sessions, domain.Session{ID: "s2", IntegrationID: "i1", AccountID: "222", RoleName: "Old", ProfileName: "old", State: domain.SessionActive})
		return nil
	})
	w := awsfiles.NewWriterAt(e.credPath, filepath.Join(filepath.Dir(e.credPath), "config"))
	_ = w.UpsertProfile("old", awsfiles.Credentials{AccessKeyID: "LIVE"}, "", false)

	if err := e.mgr.Login(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
	sessions := e.mgr.ListSessions()
	if len(sessions) != 1 || sessions[0].ID != "s1" || !sessions[0].Pinned {
		t.Fatalf("existing session should be kept with its state: %+v", sessions)
	}
	if data, _ := os.ReadFile(e.credPath); strings.Contains(string(data), "LIVE") {
		t.Fatalf("credentials of a vanished role were left behind: %s", data)
	}
}

func TestValidationOnIntegrations(t *testing.T) {
	e := newEnv(t, &fakeSSO{}, domain.Session{})
	for _, in := range []AddIntegrationInput{
		{Name: "x", StartURL: "http://insecure.example", SSORegion: "eu-west-1"},
		{Name: "x", StartURL: "https://ok.example/start", SSORegion: "not a region"},
	} {
		if _, err := e.mgr.AddIntegration(in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
}

func TestUpdateSettingsRestartsRotation(t *testing.T) {
	e := newEnv(t, &fakeSSO{}, domain.Session{})
	e.mgr.StartRotation()
	defer e.mgr.StopRotation()
	s := e.mgr.GetSettings()
	s.RotationIntervalMin = 7
	if err := e.mgr.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if !e.mgr.rotationRunning() {
		t.Fatal("rotation should keep running after a settings change")
	}
	if got := e.mgr.GetSettings().RotationIntervalMin; got != 7 {
		t.Fatalf("interval not saved: %d", got)
	}
	s.DefaultRegion = "eu west 1; calc"
	if err := e.mgr.UpdateSettings(s); err == nil {
		t.Fatal("invalid region accepted")
	}
}

func TestUpdateSettingsAlignsSessionRegions(t *testing.T) {
	e := newEnv(t, &fakeSSO{}, domain.Session{Region: "eu-west-1", State: domain.SessionInactive})
	s := e.mgr.GetSettings()
	s.DefaultRegion = "eu-west-2"
	if err := e.mgr.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	sessions := e.mgr.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("sessions: %d", len(sessions))
	}
	if sessions[0].Region != "eu-west-2" {
		t.Fatalf("session region not updated: %q", sessions[0].Region)
	}
}
