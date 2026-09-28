package sso

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sso"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/sso/types"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"

	"sessman/internal/secretstore"
	"sessman/internal/validate"
)

// ErrNotLoggedIn means there is no stored SSO token for the integration.
var ErrNotLoggedIn = errors.New("not logged in")

// ErrTokenExpired means the SSO token is expired or was rejected by the portal.
var ErrTokenExpired = errors.New("sso token expired")

// IsAuthError reports whether err means the user has to log in again.
func IsAuthError(err error) bool {
	return errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrTokenExpired)
}

// maxRoleFetchers bounds concurrent ListAccountRoles calls.
const maxRoleFetchers = 8

const (
	clientName = "sessman"
	clientType = "public"
	grantType  = "urn:ietf:params:oauth:grant-type:device_code"
	// refreshGrantType renews an access token while the Identity Center
	// portal session is still alive.
	refreshGrantType = "refresh_token"
	// accessTokenSkew refreshes slightly before wall-clock expiry so a
	// concurrent GetRoleCredentials call does not race the deadline.
	accessTokenSkew = 2 * time.Minute
)

// AccountRole is a discovered account + role pair.
type AccountRole struct {
	AccountID   string
	AccountName string
	RoleName    string
}

// RoleCredentials are temporary AWS credentials.
type RoleCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// Client performs Identity Center OIDC + portal API calls.
type Client struct {
	secrets *secretstore.Store

	mu      sync.Mutex
	oidcs   map[string]*ssooidc.Client
	portals map[string]*sso.Client
	// tokenLocks serialise read/refresh/write of each integration's token so
	// concurrent refresh_token grants cannot invalidate each other.
	tokenLocks map[string]*sync.Mutex
}

// NewClient creates an SSO client.
func NewClient(secrets *secretstore.Store) *Client {
	return &Client{
		secrets:    secrets,
		oidcs:      map[string]*ssooidc.Client{},
		portals:    map[string]*sso.Client{},
		tokenLocks: map[string]*sync.Mutex{},
	}
}

func (c *Client) tokenLock(integrationID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.tokenLocks[integrationID]
	if !ok {
		l = &sync.Mutex{}
		c.tokenLocks[integrationID] = l
	}
	return l
}

// The OIDC and portal APIs are unauthenticated (the access token travels in
// the request), so the clients are built without loading the user's shared
// AWS config, environment profile or credentials. A broken ~/.aws/config or
// AWS_PROFILE therefore cannot break SSO login, and clients are cached per region.
func (c *Client) oidc(region string) *ssooidc.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.oidcs[region]
	if !ok {
		cl = ssooidc.New(ssooidc.Options{Region: region, Credentials: aws.AnonymousCredentials{}})
		c.oidcs[region] = cl
	}
	return cl
}

func (c *Client) portal(region string) *sso.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.portals[region]
	if !ok {
		cl = sso.New(sso.Options{Region: region, Credentials: aws.AnonymousCredentials{}})
		c.portals[region] = cl
	}
	return cl
}

// wrapPortalErr maps a rejected access token onto ErrTokenExpired.
func wrapPortalErr(op string, err error) error {
	var unauth *ssotypes.UnauthorizedException
	if errors.As(err, &unauth) {
		return fmt.Errorf("%s: %w: %v", op, ErrTokenExpired, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// DeviceAuthInfo is shown to the user during SSO device login.
type DeviceAuthInfo struct {
	UserCode        string
	VerificationURI string
}

// DeviceAuthCallback is invoked when the browser login code is available.
type DeviceAuthCallback func(info DeviceAuthInfo)

// Login runs device authorization and stores the access token.
func (c *Client) Login(ctx context.Context, integrationID, startURL, region string, onDeviceAuth DeviceAuthCallback) error {
	if err := validate.StartURL(startURL); err != nil {
		return err
	}
	if err := validate.SSORegion(region); err != nil {
		return err
	}
	oidc := c.oidc(region)

	clientInfo, err := c.ensureOIDCClient(ctx, oidc, integrationID)
	if err != nil {
		return err
	}

	auth, err := oidc.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     aws.String(clientInfo.ClientID),
		ClientSecret: aws.String(clientInfo.ClientSecret),
		StartUrl:     aws.String(startURL),
	})
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	uri := aws.ToString(auth.VerificationUriComplete)
	if uri == "" {
		uri = aws.ToString(auth.VerificationUri)
	}
	userCode := aws.ToString(auth.UserCode)
	if onDeviceAuth != nil {
		onDeviceAuth(DeviceAuthInfo{
			UserCode:        userCode,
			VerificationURI: uri,
		})
	}
	_ = openBrowser(uri)

	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	if auth.ExpiresIn <= 0 {
		deadline = time.Now().Add(10 * time.Minute)
	}

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("device authorization timed out")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		out, err := oidc.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     aws.String(clientInfo.ClientID),
			ClientSecret: aws.String(clientInfo.ClientSecret),
			DeviceCode:   auth.DeviceCode,
			GrantType:    aws.String(grantType),
		})
		if err != nil {
			var pending *types.AuthorizationPendingException
			var slowdown *types.SlowDownException
			if errors.As(err, &pending) {
				continue
			}
			if errors.As(err, &slowdown) {
				interval += 5 * time.Second
				continue
			}
			msg := err.Error()
			if strings.Contains(msg, "AuthorizationPendingException") || strings.Contains(msg, "authorization_pending") {
				continue
			}
			if strings.Contains(msg, "SlowDownException") || strings.Contains(msg, "slow_down") {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("create token: %w", err)
		}

		if err := c.storeTokenLocked(integrationID, out); err != nil {
			return err
		}
		return nil
	}
}

func tokenFromCreateOutput(out *ssooidc.CreateTokenOutput) secretstore.Token {
	expiresIn := int64(out.ExpiresIn)
	if expiresIn <= 0 {
		expiresIn = 8 * 3600
	}
	return secretstore.Token{
		AccessToken:  aws.ToString(out.AccessToken),
		RefreshToken: aws.ToString(out.RefreshToken),
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	}
}

func (c *Client) storeTokenLocked(integrationID string, out *ssooidc.CreateTokenOutput) error {
	lock := c.tokenLock(integrationID)
	lock.Lock()
	defer lock.Unlock()
	return c.saveCreateToken(integrationID, out)
}

func (c *Client) saveCreateToken(integrationID string, out *ssooidc.CreateTokenOutput) error {
	token := tokenFromCreateOutput(out)
	// CreateToken rotates the refresh token: keep the previous one only if
	// this response omitted a replacement (defensive; AWS normally returns one).
	if token.RefreshToken == "" {
		if prev, err := c.secrets.GetToken(integrationID); err == nil {
			token.RefreshToken = prev.RefreshToken
		}
	}
	if err := c.secrets.SaveToken(integrationID, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	return nil
}

func (c *Client) ensureOIDCClient(ctx context.Context, oidc *ssooidc.Client, integrationID string) (secretstore.OIDCClient, error) {
	existing, err := c.secrets.GetOIDCClient(integrationID)
	if err == nil && existing.ClientID != "" && existing.ExpiresAt > time.Now().Add(24*time.Hour).Unix() {
		return existing, nil
	}

	reg, err := oidc.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String(clientName),
		ClientType: aws.String(clientType),
		Scopes:     []string{"sso:account:access"},
	})
	if err != nil {
		return secretstore.OIDCClient{}, fmt.Errorf("register client: %w", err)
	}
	info := secretstore.OIDCClient{
		ClientID:     aws.ToString(reg.ClientId),
		ClientSecret: aws.ToString(reg.ClientSecret),
		ExpiresAt:    reg.ClientSecretExpiresAt,
	}
	if err := c.secrets.SaveOIDCClient(integrationID, info); err != nil {
		return secretstore.OIDCClient{}, fmt.Errorf("save oidc client: %w", err)
	}
	return info, nil
}

// ValidToken returns a usable access token, refreshing it with the stored
// refresh token when the access token is expired or about to expire.
// When refresh fails (portal session gone, or no refresh token), it returns
// ErrTokenExpired so the caller can trigger interactive login.
func (c *Client) ValidToken(ctx context.Context, integrationID, region string) (string, error) {
	lock := c.tokenLock(integrationID)
	lock.Lock()
	defer lock.Unlock()

	t, err := c.secrets.GetToken(integrationID)
	if err != nil {
		if secretstore.IsNotFound(err) {
			return "", ErrNotLoggedIn
		}
		return "", err
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return "", ErrNotLoggedIn
	}

	deadline := time.Now().Add(accessTokenSkew).Unix()
	if t.AccessToken != "" && t.ExpiresAt > deadline {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", ErrTokenExpired
	}
	if err := validate.SSORegion(region); err != nil {
		return "", err
	}

	oidc := c.oidc(region)
	clientInfo, err := c.ensureOIDCClient(ctx, oidc, integrationID)
	if err != nil {
		return "", err
	}
	out, err := oidc.CreateToken(ctx, &ssooidc.CreateTokenInput{
		ClientId:     aws.String(clientInfo.ClientID),
		ClientSecret: aws.String(clientInfo.ClientSecret),
		GrantType:    aws.String(refreshGrantType),
		RefreshToken: aws.String(t.RefreshToken),
	})
	if err != nil {
		return "", fmt.Errorf("refresh token: %w: %v", ErrTokenExpired, err)
	}
	if err := c.saveCreateToken(integrationID, out); err != nil {
		return "", err
	}
	access := aws.ToString(out.AccessToken)
	if access == "" {
		return "", ErrTokenExpired
	}
	return access, nil
}

// ListAccountRoles discovers all account/role pairs for the logged-in user.
// Roles are fetched concurrently (bounded) since each account needs its own call.
func (c *Client) ListAccountRoles(ctx context.Context, integrationID, region string) ([]AccountRole, error) {
	token, err := c.ValidToken(ctx, integrationID, region)
	if err != nil {
		return nil, err
	}
	portal := c.portal(region)

	type account struct{ id, name string }
	var accounts []account
	var nextToken *string
	for {
		out, err := portal.ListAccounts(ctx, &sso.ListAccountsInput{
			AccessToken: aws.String(token),
			NextToken:   nextToken,
		})
		if err != nil {
			return nil, wrapPortalErr("list accounts", err)
		}
		for _, acct := range out.AccountList {
			a := account{id: aws.ToString(acct.AccountId), name: aws.ToString(acct.AccountName)}
			if a.name == "" {
				a.name = a.id
			}
			accounts = append(accounts, a)
		}
		if aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	roles := make([][]string, len(accounts))
	sem := make(chan struct{}, maxRoleFetchers)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	for i, a := range accounts {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := c.listRoles(ctx, portal, token, a.id)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			roles[i] = r
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	var results []AccountRole
	for i, a := range accounts {
		for _, role := range roles[i] {
			results = append(results, AccountRole{AccountID: a.id, AccountName: a.name, RoleName: role})
		}
	}
	return results, nil
}

func (c *Client) listRoles(ctx context.Context, portal *sso.Client, token, accountID string) ([]string, error) {
	var roles []string
	var nextToken *string
	for {
		out, err := portal.ListAccountRoles(ctx, &sso.ListAccountRolesInput{
			AccessToken: aws.String(token),
			AccountId:   aws.String(accountID),
			NextToken:   nextToken,
		})
		if err != nil {
			return nil, wrapPortalErr("list roles for "+accountID, err)
		}
		for _, r := range out.RoleList {
			roles = append(roles, aws.ToString(r.RoleName))
		}
		if aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}
	return roles, nil
}

// GetRoleCredentials fetches temporary credentials for an account role.
func (c *Client) GetRoleCredentials(ctx context.Context, integrationID, region, accountID, roleName string) (RoleCredentials, error) {
	token, err := c.ValidToken(ctx, integrationID, region)
	if err != nil {
		return RoleCredentials{}, err
	}
	out, err := c.portal(region).GetRoleCredentials(ctx, &sso.GetRoleCredentialsInput{
		AccessToken: aws.String(token),
		AccountId:   aws.String(accountID),
		RoleName:    aws.String(roleName),
	})
	if err != nil {
		return RoleCredentials{}, wrapPortalErr("get role credentials", err)
	}
	rc := out.RoleCredentials
	if rc == nil {
		return RoleCredentials{}, errors.New("get role credentials: empty response")
	}
	exp := time.UnixMilli(rc.Expiration)
	return RoleCredentials{
		AccessKeyID:     aws.ToString(rc.AccessKeyId),
		SecretAccessKey: aws.ToString(rc.SecretAccessKey),
		SessionToken:    aws.ToString(rc.SessionToken),
		Expiration:      exp,
	}, nil
}

// Logout clears stored secrets for an integration.
func (c *Client) Logout(integrationID string) error {
	return c.secrets.DeleteIntegrationSecrets(integrationID)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
