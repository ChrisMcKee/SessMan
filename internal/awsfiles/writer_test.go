package awsfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertAndRemoveProfile(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials")
	cfgPath := filepath.Join(dir, "config")

	// Seed an existing unrelated profile
	if err := os.WriteFile(credPath, []byte("[other]\naws_access_key_id = KEEP\naws_secret_access_key = KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := NewWriterAt(credPath, cfgPath)
	err := w.UpsertProfile("dev-admin", Credentials{
		AccessKeyID:     "AKIATEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
	}, "eu-west-1", true)
	if err != nil {
		t.Fatal(err)
	}

	credData, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	cred := string(credData)
	if !strings.Contains(cred, "[other]") || !strings.Contains(cred, "KEEP") {
		t.Fatalf("unrelated profile wiped: %s", cred)
	}
	if !strings.Contains(cred, "[dev-admin]") || !strings.Contains(cred, "AKIATEST") {
		t.Fatalf("profile not written: %s", cred)
	}

	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(cfgData)
	if !strings.Contains(cfg, "[profile dev-admin]") || !strings.Contains(cfg, "eu-west-1") {
		t.Fatalf("config not written: %s", cfg)
	}

	if err := w.RemoveProfile("dev-admin", true); err != nil {
		t.Fatal(err)
	}
	credData, _ = os.ReadFile(credPath)
	if strings.Contains(string(credData), "dev-admin") {
		t.Fatalf("profile not removed: %s", credData)
	}
	if !strings.Contains(string(credData), "other") {
		t.Fatalf("unrelated profile removed: %s", credData)
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials")
	cfgPath := filepath.Join(dir, "config")
	w := NewWriterAt(credPath, cfgPath)

	_ = w.UpsertProfile("p1", Credentials{AccessKeyID: "A", SecretAccessKey: "B", SessionToken: "C"}, "us-east-1", true)
	_ = w.UpsertProfile("p1", Credentials{AccessKeyID: "X", SecretAccessKey: "Y", SessionToken: "Z"}, "us-west-2", true)

	data, _ := os.ReadFile(credPath)
	s := string(data)
	if strings.Contains(s, "aws_access_key_id = A") || !strings.Contains(s, "aws_access_key_id = X") {
		t.Fatalf("expected updated keys: %s", s)
	}
	if strings.Count(s, "[p1]") != 1 {
		t.Fatalf("duplicate sections: %s", s)
	}
}

func TestSetProfileRegion(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials")
	cfgPath := filepath.Join(dir, "config")
	w := NewWriterAt(credPath, cfgPath)

	if err := w.UpsertProfile("p1", Credentials{AccessKeyID: "A", SecretAccessKey: "B", SessionToken: "C"}, "us-east-1", true); err != nil {
		t.Fatal(err)
	}
	if err := w.SetProfileRegion("p1", "eu-west-2"); err != nil {
		t.Fatal(err)
	}

	cfg, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(cfg), "region = eu-west-2") {
		t.Fatalf("region not updated: %s", cfg)
	}
	cred, _ := os.ReadFile(credPath)
	if !strings.Contains(string(cred), "aws_access_key_id = A") {
		t.Fatalf("credentials should be untouched: %s", cred)
	}
}

func TestUpsertPreservesCommentsAndNestedSettings(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials")
	cfgPath := filepath.Join(dir, "config")
	original := "# my comment\n[profile keep]\nregion = us-east-1\ns3 =\n  max_concurrent_requests = 10\n  max_queue_size = 1000\n\n; trailing\n[profile dev]\noutput = json\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	w := NewWriterAt(credPath, cfgPath)
	if err := w.UpsertProfile("dev", Credentials{AccessKeyID: "A", SecretAccessKey: "B", SessionToken: "C"}, "eu-west-1", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	want := "# my comment\n[profile keep]\nregion = us-east-1\ns3 =\n  max_concurrent_requests = 10\n  max_queue_size = 1000\n\n; trailing\n[profile dev]\noutput = json\nregion = eu-west-1\n"
	if string(data) != want {
		t.Fatalf("config changed unexpectedly:\n%s", data)
	}
	if err := w.RemoveProfile("dev", true); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "max_queue_size = 1000") || strings.Contains(string(data), "[profile dev]") {
		t.Fatalf("bad remove result:\n%s", data)
	}
}

func TestUpsertKeepsCRLFAndSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-credentials")
	link := filepath.Join(dir, "credentials")
	if err := os.WriteFile(real, []byte("[a]\r\nk = v\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	w := NewWriterAt(link, filepath.Join(dir, "config"))
	if err := w.UpsertProfile("b", Credentials{AccessKeyID: "A"}, "", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
	data, _ := os.ReadFile(real)
	if !strings.Contains(string(data), "\r\n[b]\r\n") || strings.Contains(strings.ReplaceAll(string(data), "\r\n", ""), "\n") {
		t.Fatalf("line endings not preserved: %q", data)
	}
}
