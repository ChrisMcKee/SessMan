# AWS Session Manager

Desktop app for AWS IAM Identity Center (SSO) sessions with named profiles.

Inspired by [Leapp](https://github.com/noovolari/leapp); Go + [Wails](https://wails.io/) implementation.

## Requirements

- Go 1.23+
- Node.js 18+
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

## Develop

```bash
wails dev
```

## Build

```bash
# Current OS/arch
wails build

# Explicit platforms (must build on that OS, or use CI/WSL for Linux)
wails build -platform windows/amd64
wails build -platform darwin/universal
wails build -platform linux/amd64 -tags webkit2_41
```

Linux needs GTK3 + WebKit2GTK (ABI 4.1 on modern distros). Example on Debian/Ubuntu 22.04+:

```bash
sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev
```

A FreeDesktop entry lives at [`build/linux/sessman.desktop`](build/linux/sessman.desktop).

## Usage

1. Add an SSO integration (start URL + SSO region).
2. Click **Login** and complete browser authorization.
3. Start a session to write temporary credentials to `~/.aws/credentials` under the session's profile name.
4. Use the AWS CLI/SDK with `--profile <name>`.

Workspace metadata lives under the OS config directory (`%APPDATA%/sessman` on Windows, `~/Library/Application Support/sessman` on macOS, `~/.config/sessman` on Linux). SSO secrets are AES-GCM encrypted in `secrets/`; the master key is stored in the OS credential store (Cred Manager / Keychain / Secret Service).
