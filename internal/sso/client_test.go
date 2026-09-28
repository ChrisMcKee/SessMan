package sso

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"

	"sessman/internal/secretstore"
)

func TestTokenFromCreateOutput(t *testing.T) {
	out := &ssooidc.CreateTokenOutput{
		AccessToken:  aws.String("access"),
		RefreshToken: aws.String("refresh"),
		ExpiresIn:    3600,
	}
	tok := tokenFromCreateOutput(out)
	if tok.AccessToken != "access" || tok.RefreshToken != "refresh" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if tok.ExpiresAt < time.Now().Add(50*time.Minute).Unix() {
		t.Fatalf("expiry too soon: %d", tok.ExpiresAt)
	}
}

func TestSaveCreateTokenKeepsRefreshWhenOmitted(t *testing.T) {
	store, err := secretstore.NewAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(store)
	if err := store.SaveToken("i1", secretstore.Token{
		AccessToken:  "old",
		RefreshToken: "keep-me",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.saveCreateToken("i1", &ssooidc.CreateTokenOutput{
		AccessToken: aws.String("new-access"),
		ExpiresIn:   3600,
		// RefreshToken intentionally omitted
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetToken("i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" || got.RefreshToken != "keep-me" {
		t.Fatalf("expected refresh retained, got %+v", got)
	}
}

func TestSaveCreateTokenRotatesRefresh(t *testing.T) {
	store, err := secretstore.NewAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(store)
	_ = store.SaveToken("i1", secretstore.Token{
		AccessToken:  "old",
		RefreshToken: "old-refresh",
		ExpiresAt:    1,
	})

	if err := c.saveCreateToken("i1", &ssooidc.CreateTokenOutput{
		AccessToken:  aws.String("new"),
		RefreshToken: aws.String("new-refresh"),
		ExpiresIn:    100,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetToken("i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "new-refresh" || got.AccessToken != "new" {
		t.Fatalf("unexpected: %+v", got)
	}
}
