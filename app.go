package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"awssession/internal/awsfiles"
	"awssession/internal/domain"
	"awssession/internal/eksmgr"
	"awssession/internal/secretstore"
	"awssession/internal/sessionmgr"
	"awssession/internal/ssmmgr"
	"awssession/internal/sso"
	"awssession/internal/workspace"
)

// App is the Wails-bound application API.
type App struct {
	ctx context.Context
	mgr *sessionmgr.Manager
	// initErr is why startup failed; reported by every API call so the UI can
	// show the real cause instead of a generic message.
	initErr error
}

// NewApp creates a new App.
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	fail := func(what string, err error) {
		runtime.LogErrorf(ctx, "%s: %v", what, err)
		a.initErr = fmt.Errorf("%s: %w", what, err)
	}
	store, err := workspace.NewStore()
	if err != nil {
		fail("workspace store", err)
		return
	}
	if backup := store.RecoveredFrom(); backup != "" {
		runtime.LogWarningf(ctx, "workspace file was unreadable; moved to %s and reset", backup)
	}
	awsWriter, err := awsfiles.NewWriter()
	if err != nil {
		fail("aws files", err)
		return
	}
	secrets, err := secretstore.New()
	if err != nil {
		fail("secret store (OS credential store unavailable?)", err)
		return
	}
	ssoClient := sso.NewClient(secrets)
	a.mgr = sessionmgr.New(store, awsWriter, ssoClient)
	a.mgr.SetOnChange(func() {
		runtime.EventsEmit(a.ctx, "workspace:updated")
		runtime.EventsEmit(a.ctx, "session:updated")
	})
	a.mgr.SetOnAuthRequired(func(integrationID string) {
		runtime.EventsEmit(a.ctx, "auth:required", integrationID)
	})
	a.mgr.SetOnDeviceAuth(func(info sso.DeviceAuthInfo) {
		runtime.EventsEmit(a.ctx, "auth:device-code", map[string]string{
			"userCode":        info.UserCode,
			"verificationUri": info.VerificationURI,
		})
	})
	a.mgr.StartRotation()
}

func (a *App) shutdown(ctx context.Context) {
	if a.mgr != nil {
		a.mgr.StopRotation()
	}
}

func (b *App) beforeClose(ctx context.Context) (prevent bool) {
	dialog, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:    runtime.QuestionDialog,
		Title:   "Quit?",
		Message: "Are you sure you want to quit?",
	})
	if err != nil {
		return false
	}
	return dialog != "Yes"
}

func (a *App) onSecondInstanceLaunch(_ options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.Show(a.ctx)
}

func (a *App) requireMgr() error {
	if a.mgr == nil {
		if a.initErr != nil {
			return fmt.Errorf("application failed to initialize: %w", a.initErr)
		}
		return fmt.Errorf("application not initialized")
	}
	return nil
}

// resolveRegion picks the explicit region, else the session's, else the default.
func (a *App) resolveRegion(sess domain.Session, region string) string {
	if region = strings.TrimSpace(region); region != "" {
		return region
	}
	if sess.Region != "" {
		return sess.Region
	}
	return a.mgr.GetSettings().DefaultRegion
}

// ListIntegrations returns all SSO integrations.
func (a *App) ListIntegrations() ([]domain.Integration, error) {
	if err := a.requireMgr(); err != nil {
		return nil, err
	}
	return a.mgr.ListIntegrations(), nil
}

// AddIntegration creates a new SSO integration.
func (a *App) AddIntegration(in sessionmgr.AddIntegrationInput) (domain.Integration, error) {
	if err := a.requireMgr(); err != nil {
		return domain.Integration{}, err
	}
	return a.mgr.AddIntegration(in)
}

// UpdateIntegration updates an existing integration.
func (a *App) UpdateIntegration(id string, in sessionmgr.AddIntegrationInput) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.UpdateIntegration(id, in)
}

// DeleteIntegration removes an integration.
func (a *App) DeleteIntegration(id string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.DeleteIntegration(id)
}

// Login starts SSO device authorization for an integration.
func (a *App) Login(integrationID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.Login(a.ctx, integrationID)
}

// CancelLogin aborts a pending SSO device-authorization login.
func (a *App) CancelLogin(integrationID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	a.mgr.CancelLogin(integrationID)
	return nil
}

// Logout clears SSO credentials for an integration.
func (a *App) Logout(integrationID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.Logout(integrationID)
}

// ListSessions returns all discovered sessions.
func (a *App) ListSessions() ([]domain.Session, error) {
	if err := a.requireMgr(); err != nil {
		return nil, err
	}
	return a.mgr.ListSessions(), nil
}

// StartSession writes temporary credentials for a named profile.
func (a *App) StartSession(sessionID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.StartSession(a.ctx, sessionID)
}

// StopSession removes temporary credentials for a session.
func (a *App) StopSession(sessionID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.StopSession(sessionID)
}

// SetProfileName renames the AWS profile for a session.
func (a *App) SetProfileName(sessionID, profileName string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.SetProfileName(sessionID, profileName)
}

// SetPinned pins or unpins a session in the list.
func (a *App) SetPinned(sessionID string, pinned bool) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.SetPinned(sessionID, pinned)
}

// GetSettings returns app settings.
func (a *App) GetSettings() (domain.Settings, error) {
	if err := a.requireMgr(); err != nil {
		return domain.Settings{}, err
	}
	return a.mgr.GetSettings(), nil
}

// UpdateSettings persists app settings.
func (a *App) UpdateSettings(s domain.Settings) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	return a.mgr.UpdateSettings(s)
}

// SSMPluginStatus reports whether AWS CLI and Session Manager plugin are available.
func (a *App) SSMPluginStatus() ssmmgr.SetupStatus {
	return ssmmgr.CheckSetup()
}

// ListSSMInstances lists online SSM instances for a session (starts the session if needed).
func (a *App) ListSSMInstances(sessionID, region string) ([]ssmmgr.Instance, error) {
	if err := a.requireMgr(); err != nil {
		return nil, err
	}
	sess, err := a.mgr.EnsureActive(a.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	region = a.resolveRegion(sess, region)
	return ssmmgr.ListOnline(a.ctx, sess.ProfileName, region)
}

// ConnectSSM opens a terminal with aws ssm start-session for the instance.
func (a *App) ConnectSSM(sessionID, region, instanceID string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return fmt.Errorf("instance id is required")
	}
	sess, err := a.mgr.EnsureActive(a.ctx, sessionID)
	if err != nil {
		return err
	}
	region = a.resolveRegion(sess, region)
	return ssmmgr.Connect(sess.ProfileName, region, instanceID)
}

// EKSSetupStatus reports whether AWS CLI is available for kubeconfig updates.
func (a *App) EKSSetupStatus() eksmgr.SetupStatus {
	return eksmgr.CheckSetup()
}

// ListEKSClusters lists EKS clusters for a session (starts the session if needed).
func (a *App) ListEKSClusters(sessionID, region string) ([]eksmgr.Cluster, error) {
	if err := a.requireMgr(); err != nil {
		return nil, err
	}
	sess, err := a.mgr.EnsureActive(a.ctx, sessionID)
	if err != nil {
		return nil, err
	}
	region = a.resolveRegion(sess, region)
	return eksmgr.ListClusters(a.ctx, sess.ProfileName, region)
}

// UpdateEKSKubeconfig writes the cluster into the local kubeconfig via AWS CLI.
func (a *App) UpdateEKSKubeconfig(sessionID, region, clusterName string) error {
	if err := a.requireMgr(); err != nil {
		return err
	}
	sess, err := a.mgr.EnsureActive(a.ctx, sessionID)
	if err != nil {
		return err
	}
	region = a.resolveRegion(sess, region)
	return eksmgr.UpdateKubeconfig(sess.ProfileName, region, clusterName)
}
