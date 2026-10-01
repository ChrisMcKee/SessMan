package main

import "testing"

func TestProductVersion(t *testing.T) {
	got := productVersion([]byte(`{"info":{"productVersion":" 1.2.3 "}}`))
	if got != "1.2.3" {
		t.Fatalf("productVersion() = %q, want 1.2.3", got)
	}
	if productVersion([]byte(`not json`)) != "" {
		t.Fatal("invalid json should yield an empty version")
	}
	if productVersion(wailsConfig) == "" {
		t.Fatal("embedded wails.json should include a product version")
	}
}
