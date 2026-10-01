package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed wails.json
var wailsConfig []byte

// Version returns the product version from wails.json. Release builds rewrite
// that file from the git tag before compile, so the embedded copy matches the
// binary's product version.
func (a *App) Version() string {
	return productVersion(wailsConfig)
}

func productVersion(raw []byte) string {
	var cfg struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Info.ProductVersion)
}
