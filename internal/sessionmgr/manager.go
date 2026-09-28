package sessionmgr

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"sessman/internal/awsfiles"
	"sessman/internal/domain"
	"sessman/internal/sso"
	"sessman/internal/validate"
	"sessman/internal/workspace"
)

const (
	// credTimeout bounds a single credential fetch so a hung request cannot
	// stall rotation or a user-initiated start.
	credTimeout = 30 * time.Second
	// discoveryTimeout bounds account/role discovery after login.
	discoveryTimeout = 2 * time.Minute
	// rotationMargin is extra headroom before expiry when deciding to refresh.
	rotationMargin = 5 * time.Minute
	// defaultRotationInterval is used when settings hold no valid interval.
	defaultRotationInterval = 20 * time.Minute
)

// errInterrupted means the session was stopped or changed while it was starting.
var errInterrupted = errors.New("session start was interrupted")

// ssoAPI is the subset of the SSO client the manager depends on.
type ssoAPI interface {
	Login(ctx context.Context, integrationID, startURL, region string, onDeviceAuth sso.DeviceAuthCallback) error
	ListAccountRoles(ctx context.Context, integrationID, region string) ([]sso.AccountRole, error)
	GetRoleCredentials(ctx context.Context, integrationID, region, accountID, roleName string) (sso.RoleCredentials, error)
	Logout(integrationID string) error
}

// flight is an in-progress session start shared by concurrent callers.
type flight struct {
	done chan struct{}
	err  error
}

// Manager orchestrates integrations, sessions, and credential files.
type Manager struct {
	store *workspace.Store
	aws   *awsfiles.Writer
	sso   ssoAPI

	// mu serialises credential-file/state writes. It is never held across a
	// network call.
	mu sync.Mutex

	fmu     sync.Mutex
	flights map[string]*flight

	loginMu sync.Mutex
	logins  map[string]context.CancelFunc

	rotMu     sync.Mutex
	rotCancel context.CancelFunc

	onChange       func()
	onAuthRequired func(integrationID string)
	onDeviceAuth   func(info sso.DeviceAuthInfo)
}

// New creates a session manager.
func New(store *workspace.Store, aws *awsfiles.Writer, ssoClient ssoAPI) *Manager {
	return &Manager{
		store:   store,
		aws:     aws,
		sso:     ssoClient,
		flights: map[string]*flight{},
		logins:  map[string]context.CancelFunc{},
	}
}

// SetOnChange registers a callback when workspace state changes.
func (m *Manager) SetOnChange(fn func()) {
	m.onChange = fn
}

// SetOnAuthRequired registers a callback when re-login is needed.
func (m *Manager) SetOnAuthRequired(fn func(integrationID string)) {
	m.onAuthRequired = fn
}

// SetOnDeviceAuth registers a callback when the SSO user code is ready.
func (m *Manager) SetOnDeviceAuth(fn func(info sso.DeviceAuthInfo)) {
	m.onDeviceAuth = fn
}

func (m *Manager) notify() {
	if m.onChange != nil {
		m.onChange()
	}
}

func rotationInterval(s domain.Settings) time.Duration {
	if s.RotationIntervalMin <= 0 {
		return defaultRotationInterval
	}
	return time.Duration(s.RotationIntervalMin) * time.Minute
}

// StartRotation launches (or restarts) background credential refresh using
// the current rotation interval setting. An immediate pass runs first so a
// cold start after overnight expiry does not leave Active sessions (and a
// LoggedIn integration badge) pointing at dead credentials until the first tick.
func (m *Manager) StartRotation() {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	if m.rotCancel != nil {
		m.rotCancel()
	}
	interval := rotationInterval(m.store.Get().Settings)
	ctx, cancel := context.WithCancel(context.Background())
	m.rotCancel = cancel
	go func() {
		_ = m.RotateActive(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = m.RotateActive(ctx)
			}
		}
	}()
}

// StopRotation stops the background rotator.
func (m *Manager) StopRotation() {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	if m.rotCancel != nil {
		m.rotCancel()
		m.rotCancel = nil
	}
}

func (m *Manager) rotationRunning() bool {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	return m.rotCancel != nil
}

// ListIntegrations returns integrations.
func (m *Manager) ListIntegrations() []domain.Integration {
	return m.store.Get().Integrations
}

// ListSessions returns sessions with pinned entries first.
func (m *Manager) ListSessions() []domain.Session {
	sessions := append([]domain.Session(nil), m.store.Get().Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned
		}
		if sessions[i].AccountName != sessions[j].AccountName {
			return strings.ToLower(sessions[i].AccountName) < strings.ToLower(sessions[j].AccountName)
		}
		return strings.ToLower(sessions[i].RoleName) < strings.ToLower(sessions[j].RoleName)
	})
	return sessions
}

// GetSettings returns settings.
func (m *Manager) GetSettings() domain.Settings {
	return m.store.Get().Settings
}

// UpdateSettings persists settings and applies a changed rotation interval.
// Session regions are aligned to DefaultRegion so the listing stays in sync
// when the user changes the default (including a re-save that repairs stale
// session rows created under an older default).
func (m *Manager) UpdateSettings(s domain.Settings) error {
	s.DefaultRegion = strings.TrimSpace(s.DefaultRegion)
	if s.DefaultRegion == "" {
		s.DefaultRegion = domain.DefaultSettings().DefaultRegion
	}
	if err := validate.Region(s.DefaultRegion); err != nil {
		return err
	}
	if s.RotationIntervalMin <= 0 {
		s.RotationIntervalMin = int(defaultRotationInterval / time.Minute)
	}
	intervalChanged := false
	var profilesToSync []string
	err := m.store.Update(func(ws *domain.Workspace) error {
		intervalChanged = ws.Settings.RotationIntervalMin != s.RotationIntervalMin
		ws.Settings = s
		for i := range ws.Sessions {
			if ws.Sessions[i].Region == s.DefaultRegion {
				continue
			}
			ws.Sessions[i].Region = s.DefaultRegion
			if s.SyncProfileRegion && ws.Sessions[i].State == domain.SessionActive && ws.Sessions[i].ProfileName != "" {
				profilesToSync = append(profilesToSync, ws.Sessions[i].ProfileName)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(profilesToSync) > 0 && m.aws != nil {
		m.mu.Lock()
		for _, profile := range profilesToSync {
			_ = m.aws.SetProfileRegion(profile, s.DefaultRegion)
		}
		m.mu.Unlock()
	}
	if intervalChanged && m.rotationRunning() {
		m.StartRotation()
	}
	m.notify()
	return nil
}

// AddIntegrationInput is the payload for creating an integration.
type AddIntegrationInput struct {
	Name      string `json:"name"`
	StartURL  string `json:"startUrl"`
	SSORegion string `json:"ssoRegion"`
}

func (in *AddIntegrationInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.StartURL = strings.TrimSpace(in.StartURL)
	in.SSORegion = strings.TrimSpace(in.SSORegion)
	if in.Name == "" || in.StartURL == "" || in.SSORegion == "" {
		return fmt.Errorf("name, startUrl, and ssoRegion are required")
	}
	if err := validate.StartURL(in.StartURL); err != nil {
		return err
	}
	return validate.SSORegion(in.SSORegion)
}

// AddIntegration creates a new SSO integration.
// When the workspace still uses the factory default region, the SSO region
// chosen here becomes the default session region — users typically pick the
// region they want for both Identity Center and named profiles.
func (m *Manager) AddIntegration(in AddIntegrationInput) (domain.Integration, error) {
	if err := in.normalize(); err != nil {
		return domain.Integration{}, err
	}
	integ := domain.Integration{
		ID:        uuid.NewString(),
		Name:      in.Name,
		StartURL:  in.StartURL,
		SSORegion: in.SSORegion,
		Status:    domain.IntegrationLoggedOut,
	}
	err := m.store.Update(func(ws *domain.Workspace) error {
		ws.Integrations = append(ws.Integrations, integ)
		factory := domain.DefaultSettings().DefaultRegion
		if ws.Settings.DefaultRegion == factory && in.SSORegion != factory {
			ws.Settings.DefaultRegion = in.SSORegion
			for i := range ws.Sessions {
				ws.Sessions[i].Region = in.SSORegion
			}
		}
		return nil
	})
	if err != nil {
		return domain.Integration{}, err
	}
	m.notify()
	return integ, nil
}

// UpdateIntegration updates an existing integration.
func (m *Manager) UpdateIntegration(id string, in AddIntegrationInput) error {
	if err := in.normalize(); err != nil {
		return err
	}
	err := m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Integrations {
			if ws.Integrations[i].ID == id {
				ws.Integrations[i].Name = in.Name
				ws.Integrations[i].StartURL = in.StartURL
				ws.Integrations[i].SSORegion = in.SSORegion
				return nil
			}
		}
		return fmt.Errorf("integration not found")
	})
	if err == nil {
		m.notify()
	}
	return err
}

// stopActiveFor stops every active session of an integration (best effort).
func (m *Manager) stopActiveFor(integrationID string) {
	for _, sess := range m.store.Get().Sessions {
		if sess.IntegrationID == integrationID && sess.State == domain.SessionActive {
			_ = m.StopSession(sess.ID)
		}
	}
}

// DeleteIntegration removes an integration and its sessions.
func (m *Manager) DeleteIntegration(id string) error {
	m.CancelLogin(id)
	m.stopActiveFor(id)
	_ = m.sso.Logout(id)
	err := m.store.Update(func(ws *domain.Workspace) error {
		integs := make([]domain.Integration, 0, len(ws.Integrations))
		for _, i := range ws.Integrations {
			if i.ID != id {
				integs = append(integs, i)
			}
		}
		ws.Integrations = integs
		sessions := make([]domain.Session, 0, len(ws.Sessions))
		for _, s := range ws.Sessions {
			if s.IntegrationID != id {
				sessions = append(sessions, s)
			}
		}
		ws.Sessions = sessions
		return nil
	})
	if err == nil {
		m.notify()
	}
	return err
}

// Login authenticates an integration and discovers sessions. Only one login
// per integration may run at a time; CancelLogin aborts a pending one.
func (m *Manager) Login(ctx context.Context, integrationID string) error {
	integ, err := m.findIntegration(integrationID)
	if err != nil {
		return err
	}

	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.loginMu.Lock()
	if _, busy := m.logins[integrationID]; busy {
		m.loginMu.Unlock()
		return fmt.Errorf("login already in progress")
	}
	m.logins[integrationID] = cancel
	m.loginMu.Unlock()
	defer func() {
		m.loginMu.Lock()
		delete(m.logins, integrationID)
		m.loginMu.Unlock()
	}()

	if err := m.sso.Login(lctx, integrationID, integ.StartURL, integ.SSORegion, m.onDeviceAuth); err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("login cancelled")
		}
		return err
	}
	dctx, dcancel := context.WithTimeout(lctx, discoveryTimeout)
	defer dcancel()
	roles, err := m.sso.ListAccountRoles(dctx, integrationID, integ.SSORegion)
	if err != nil {
		_ = m.setIntegrationStatus(integrationID, domain.IntegrationExpired)
		return err
	}
	if err := m.upsertSessions(integrationID, roles); err != nil {
		return err
	}
	if err := m.setIntegrationStatus(integrationID, domain.IntegrationLoggedIn); err != nil {
		return err
	}
	m.resumeAfterLogin(lctx, integrationID)
	m.notify()
	return nil
}

// resumeAfterLogin restarts sessions that were left Error/Pending/lapsed by an
// SSO expiry, and clears leftover LastError on still-valid Active sessions.
func (m *Manager) resumeAfterLogin(ctx context.Context, integrationID string) {
	ws := m.store.Get()
	var restart, clearErr []string
	now := time.Now()
	for _, sess := range ws.Sessions {
		if sess.IntegrationID != integrationID {
			continue
		}
		switch sess.State {
		case domain.SessionError, domain.SessionPending:
			restart = append(restart, sess.ID)
		case domain.SessionActive:
			if credentialsLapsed(sess, now) {
				restart = append(restart, sess.ID)
			} else if sess.LastError != "" {
				clearErr = append(clearErr, sess.ID)
			}
		}
	}
	if len(clearErr) > 0 {
		ids := map[string]bool{}
		for _, id := range clearErr {
			ids[id] = true
		}
		_ = m.store.Update(func(ws *domain.Workspace) error {
			for i := range ws.Sessions {
				if ids[ws.Sessions[i].ID] {
					ws.Sessions[i].LastError = ""
				}
			}
			return nil
		})
	}
	var wg sync.WaitGroup
	for _, id := range restart {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.StartSession(ctx, id)
		}()
	}
	wg.Wait()
}

// CancelLogin aborts a pending device-authorization login, if any.
func (m *Manager) CancelLogin(integrationID string) {
	m.loginMu.Lock()
	cancel := m.logins[integrationID]
	m.loginMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Logout clears SSO token and marks sessions inactive for the integration.
func (m *Manager) Logout(integrationID string) error {
	m.CancelLogin(integrationID)
	m.stopActiveFor(integrationID)
	_ = m.sso.Logout(integrationID)
	err := m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Integrations {
			if ws.Integrations[i].ID == integrationID {
				ws.Integrations[i].Status = domain.IntegrationLoggedOut
			}
		}
		return nil
	})
	if err == nil {
		m.notify()
	}
	return err
}

// StartSession fetches credentials and writes the named profile. Concurrent
// starts of the same session share one fetch.
func (m *Manager) StartSession(ctx context.Context, sessionID string) error {
	return m.singleFlight(ctx, sessionID, func() error {
		return m.startSession(ctx, sessionID, false)
	})
}

func (m *Manager) singleFlight(ctx context.Context, id string, fn func() error) error {
	m.fmu.Lock()
	if f, ok := m.flights[id]; ok {
		m.fmu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	m.flights[id] = f
	m.fmu.Unlock()

	f.err = fn()

	m.fmu.Lock()
	delete(m.flights, id)
	m.fmu.Unlock()
	close(f.done)
	return f.err
}

// startSession does the work of StartSession. When rotating, the session is
// already Active: it keeps serving its current credentials (state untouched)
// until the refresh succeeds, so a transient failure is retried on the next
// tick instead of stranding the session.
func (m *Manager) startSession(ctx context.Context, sessionID string, rotating bool) error {
	sess, integ, err := m.findSession(sessionID)
	if err != nil {
		return err
	}
	if !rotating {
		_ = m.setSessionState(sessionID, domain.SessionPending, "", "")
		m.notify()
	}

	fctx, cancel := context.WithTimeout(ctx, credTimeout)
	creds, err := m.sso.GetRoleCredentials(fctx, integ.ID, integ.SSORegion, sess.AccountID, sess.RoleName)
	cancel()
	if err != nil {
		if sso.IsAuthError(err) {
			_ = m.setIntegrationStatus(integ.ID, domain.IntegrationExpired)
			if m.onAuthRequired != nil {
				m.onAuthRequired(integ.ID)
			}
		}
		m.recordFailure(sessionID, err, rotating)
		m.notify()
		return err
	}
	// A successful fetch (including after a silent SSO token refresh) means
	// the integration is usable again.
	if integ.Status != domain.IntegrationLoggedIn {
		_ = m.setIntegrationStatus(integ.ID, domain.IntegrationLoggedIn)
	}

	m.mu.Lock()
	err = m.commitCredentials(sessionID, creds, rotating)
	m.mu.Unlock()
	if err != nil && !errors.Is(err, errInterrupted) {
		m.recordFailure(sessionID, err, rotating)
	}
	m.notify()
	return err
}

// commitCredentials writes the profile and marks the session Active. Caller holds m.mu.
func (m *Manager) commitCredentials(sessionID string, creds sso.RoleCredentials, rotating bool) error {
	cur, err := m.GetSession(sessionID)
	if err != nil {
		return err
	}
	// The session was stopped (or otherwise changed) while we were fetching.
	want := domain.SessionPending
	if rotating {
		want = domain.SessionActive
	}
	if cur.State != want {
		if rotating {
			return nil
		}
		return errInterrupted
	}

	ws := m.store.Get()
	region := cur.Region
	if region == "" {
		region = ws.Settings.DefaultRegion
	}
	if err := m.aws.UpsertProfile(cur.ProfileName, awsfiles.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}, region, ws.Settings.SyncProfileRegion); err != nil {
		return err
	}

	exp := creds.Expiration.UTC().Format(time.RFC3339)
	return m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Sessions {
			if ws.Sessions[i].ID == sessionID {
				ws.Sessions[i].State = domain.SessionActive
				ws.Sessions[i].ExpiresAt = exp
				ws.Sessions[i].LastError = ""
				ws.Sessions[i].Region = region
			}
		}
		if !contains(ws.ManagedProfiles, cur.ProfileName) {
			ws.ManagedProfiles = append(ws.ManagedProfiles, cur.ProfileName)
		}
		return nil
	})
}

// recordFailure notes a failed start/refresh. A failed rotation leaves an
// Active session Active (so it is retried) unless its credentials have lapsed.
func (m *Manager) recordFailure(sessionID string, cause error, rotating bool) {
	if !rotating {
		_ = m.setSessionState(sessionID, domain.SessionError, cause.Error(), "")
		return
	}
	_ = m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Sessions {
			s := &ws.Sessions[i]
			if s.ID != sessionID || s.State != domain.SessionActive {
				continue
			}
			s.LastError = cause.Error()
			if exp, err := time.Parse(time.RFC3339, s.ExpiresAt); err == nil && !exp.After(time.Now()) {
				s.State = domain.SessionError
			}
		}
		return nil
	})
}

// StopSession removes profile credentials and marks inactive.
func (m *Manager) StopSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, _, err := m.findSession(sessionID)
	if err != nil {
		return err
	}
	_ = m.aws.RemoveProfile(sess.ProfileName, true)
	err = m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Sessions {
			if ws.Sessions[i].ID == sessionID {
				ws.Sessions[i].State = domain.SessionInactive
				ws.Sessions[i].ExpiresAt = ""
				ws.Sessions[i].LastError = ""
			}
		}
		return nil
	})
	if err == nil {
		m.notify()
	}
	return err
}

// SetProfileName updates the AWS profile name for a session.
func (m *Manager) SetProfileName(sessionID, profileName string) error {
	profileName = sanitizeProfile(profileName)
	if profileName == "" {
		return fmt.Errorf("profile name is required")
	}
	if err := validate.Profile(profileName); err != nil {
		return err
	}
	err := m.store.Update(func(ws *domain.Workspace) error {
		for _, s := range ws.Sessions {
			if s.ID != sessionID && strings.EqualFold(s.ProfileName, profileName) {
				return fmt.Errorf("profile name already in use")
			}
		}
		for i := range ws.Sessions {
			if ws.Sessions[i].ID == sessionID {
				if ws.Sessions[i].State == domain.SessionActive {
					return fmt.Errorf("stop the session before renaming its profile")
				}
				ws.Sessions[i].ProfileName = profileName
				return nil
			}
		}
		return fmt.Errorf("session not found")
	})
	if err == nil {
		m.notify()
	}
	return err
}

// SetPinned pins or unpins a session.
func (m *Manager) SetPinned(sessionID string, pinned bool) error {
	err := m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Sessions {
			if ws.Sessions[i].ID == sessionID {
				ws.Sessions[i].Pinned = pinned
				return nil
			}
		}
		return fmt.Errorf("session not found")
	})
	if err == nil {
		m.notify()
	}
	return err
}

// GetSession returns a session by id.
func (m *Manager) GetSession(sessionID string) (domain.Session, error) {
	for _, s := range m.store.Get().Sessions {
		if s.ID == sessionID {
			return s, nil
		}
	}
	return domain.Session{}, fmt.Errorf("session not found")
}

// EnsureActive starts the session if it is not already active with usable
// credentials. An Active session whose ExpiresAt has lapsed (or is missing)
// is refreshed — otherwise SSM/EKS calls hit ExpiredTokenException while the
// UI still shows Logged In / Active.
func (m *Manager) EnsureActive(ctx context.Context, sessionID string) (domain.Session, error) {
	sess, err := m.GetSession(sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if sess.State == domain.SessionActive && !credentialsLapsed(sess, time.Now()) {
		return sess, nil
	}
	if err := m.StartSession(ctx, sessionID); err != nil {
		return domain.Session{}, err
	}
	return m.GetSession(sessionID)
}

// credentialsLapsed reports whether the session's stored expiry is missing or
// not strictly in the future.
func credentialsLapsed(sess domain.Session, now time.Time) bool {
	if sess.ExpiresAt == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil {
		return true
	}
	return !exp.After(now)
}

// RotateActive refreshes active sessions whose credentials would lapse before
// the next rotation tick (plus a safety margin).
func (m *Manager) RotateActive(ctx context.Context) error {
	ws := m.store.Get()
	lead := rotationInterval(ws.Settings) + rotationMargin
	now := time.Now()
	var firstErr error
	for _, sess := range ws.Sessions {
		if ctx.Err() != nil {
			break
		}
		if sess.State != domain.SessionActive || !needsRefresh(sess, now, lead) {
			continue
		}
		id := sess.ID
		err := m.singleFlight(ctx, id, func() error { return m.startSession(ctx, id, true) })
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func needsRefresh(sess domain.Session, now time.Time, lead time.Duration) bool {
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil {
		return true
	}
	return !exp.After(now.Add(lead))
}

func (m *Manager) findIntegration(id string) (domain.Integration, error) {
	for _, i := range m.store.Get().Integrations {
		if i.ID == id {
			return i, nil
		}
	}
	return domain.Integration{}, fmt.Errorf("integration not found")
}

func (m *Manager) findSession(id string) (domain.Session, domain.Integration, error) {
	ws := m.store.Get()
	var sess domain.Session
	found := false
	for _, s := range ws.Sessions {
		if s.ID == id {
			sess = s
			found = true
			break
		}
	}
	if !found {
		return domain.Session{}, domain.Integration{}, fmt.Errorf("session not found")
	}
	for _, i := range ws.Integrations {
		if i.ID == sess.IntegrationID {
			return sess, i, nil
		}
	}
	return domain.Session{}, domain.Integration{}, fmt.Errorf("integration not found for session")
}

func (m *Manager) setIntegrationStatus(id string, status domain.IntegrationStatus) error {
	return m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Integrations {
			if ws.Integrations[i].ID == id {
				ws.Integrations[i].Status = status
				return nil
			}
		}
		return fmt.Errorf("integration not found")
	})
}

func (m *Manager) setSessionState(id string, state domain.SessionState, lastErr string, exp string) error {
	return m.store.Update(func(ws *domain.Workspace) error {
		for i := range ws.Sessions {
			if ws.Sessions[i].ID == id {
				ws.Sessions[i].State = state
				ws.Sessions[i].LastError = lastErr
				ws.Sessions[i].ExpiresAt = exp
				return nil
			}
		}
		return fmt.Errorf("session not found")
	})
}

// upsertSessions reconciles the integration's sessions with the discovered
// roles atomically, so concurrent edits (pin, rename, state) are not lost and
// profile-name uniqueness is checked against current data.
func (m *Manager) upsertSessions(integrationID string, roles []sso.AccountRole) error {
	var removed []domain.Session
	err := m.store.Update(func(ws *domain.Workspace) error {
		existing := map[string]domain.Session{}
		usedProfiles := map[string]bool{}
		for _, s := range ws.Sessions {
			if s.IntegrationID == integrationID {
				existing[s.AccountID+"/"+s.RoleName] = s
			} else {
				usedProfiles[strings.ToLower(s.ProfileName)] = true
			}
		}

		newSessions := make([]domain.Session, 0, len(roles))
		kept := map[string]bool{}
		for _, r := range roles {
			key := r.AccountID + "/" + r.RoleName
			if kept[key] {
				continue
			}
			kept[key] = true
			if prev, ok := existing[key]; ok {
				// Keep region aligned with the current default on every sync so
				// changing Settings (or seeding it from Add Integration) shows up
				// without requiring a separate settings save.
				prev.Region = ws.Settings.DefaultRegion
				newSessions = append(newSessions, prev)
				usedProfiles[strings.ToLower(prev.ProfileName)] = true
				continue
			}
			profile := uniqueProfile(defaultProfileName(r.AccountName, r.RoleName), usedProfiles)
			usedProfiles[strings.ToLower(profile)] = true
			newSessions = append(newSessions, domain.Session{
				ID:            uuid.NewString(),
				IntegrationID: integrationID,
				AccountID:     r.AccountID,
				AccountName:   r.AccountName,
				RoleName:      r.RoleName,
				ProfileName:   profile,
				Region:        ws.Settings.DefaultRegion,
				State:         domain.SessionInactive,
			})
		}
		for key, s := range existing {
			if !kept[key] {
				removed = append(removed, s)
			}
		}

		others := make([]domain.Session, 0, len(ws.Sessions))
		for _, s := range ws.Sessions {
			if s.IntegrationID != integrationID {
				others = append(others, s)
			}
		}
		ws.Sessions = append(others, newSessions...)
		return nil
	})
	if err != nil {
		return err
	}
	// Roles that vanished must not leave live credentials behind in ~/.aws.
	if m.aws != nil {
		m.mu.Lock()
		for _, s := range removed {
			if s.State == domain.SessionActive {
				_ = m.aws.RemoveProfile(s.ProfileName, true)
			}
		}
		m.mu.Unlock()
	}
	return nil
}

var nonProfile = regexp.MustCompile(`[^a-zA-Z0-9+=,.@_-]+`)

func sanitizeProfile(name string) string {
	name = strings.TrimSpace(name)
	name = nonProfile.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	return name
}

func defaultProfileName(accountName, roleName string) string {
	base := strings.ToLower(sanitizeProfile(accountName + "-" + roleName))
	if base == "" {
		base = "profile"
	}
	if len(base) > 120 { // leave room for a uniqueness suffix within validate.Profile's limit
		base = strings.TrimRight(base[:120], "-")
	}
	return base
}

func uniqueProfile(base string, used map[string]bool) string {
	candidate := base
	i := 2
	for used[strings.ToLower(candidate)] {
		candidate = fmt.Sprintf("%s-%d", base, i)
		i++
	}
	return candidate
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
