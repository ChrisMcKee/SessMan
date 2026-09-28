package domain

// IntegrationStatus represents SSO login state for an integration.
type IntegrationStatus string

const (
	IntegrationLoggedOut IntegrationStatus = "LoggedOut"
	IntegrationLoggedIn  IntegrationStatus = "LoggedIn"
	IntegrationExpired   IntegrationStatus = "Expired"
)

// SessionState represents whether credentials are active for a session.
type SessionState string

const (
	SessionInactive SessionState = "Inactive"
	SessionActive   SessionState = "Active"
	SessionPending  SessionState = "Pending"
	SessionError    SessionState = "Error"
)

// Integration is an AWS IAM Identity Center portal configuration.
type Integration struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	StartURL  string            `json:"startUrl"`
	SSORegion string            `json:"ssoRegion"`
	Status    IntegrationStatus `json:"status"`
}

// Session is one account + role pair discovered from an integration.
type Session struct {
	ID            string       `json:"id"`
	IntegrationID string       `json:"integrationId"`
	AccountID     string       `json:"accountId"`
	AccountName   string       `json:"accountName"`
	RoleName      string       `json:"roleName"`
	ProfileName   string       `json:"profileName"`
	Region        string       `json:"region"`
	State         SessionState `json:"state"`
	Pinned        bool         `json:"pinned,omitempty"`
	// ExpiresAt is RFC3339 when credentials expire; empty if inactive.
	ExpiresAt string `json:"expiresAt,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

// Settings holds user preferences.
type Settings struct {
	DefaultRegion       string `json:"defaultRegion"`
	RotationIntervalMin int    `json:"rotationIntervalMin"`
	SyncProfileRegion   bool   `json:"syncProfileRegion"`
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		DefaultRegion:       "eu-west-1",
		RotationIntervalMin: 20,
		SyncProfileRegion:   true,
	}
}

// Workspace is the persisted app configuration (non-secret).
type Workspace struct {
	Integrations []Integration `json:"integrations"`
	Sessions     []Session     `json:"sessions"`
	Settings     Settings      `json:"settings"`
	// ManagedProfiles tracks profile names owned by this app.
	ManagedProfiles []string `json:"managedProfiles"`
}

// NewWorkspace returns an empty workspace with defaults.
func NewWorkspace() Workspace {
	return Workspace{
		Integrations:    []Integration{},
		Sessions:        []Session{},
		Settings:        DefaultSettings(),
		ManagedProfiles: []string{},
	}
}
