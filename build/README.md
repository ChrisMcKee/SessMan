# Build Directory

The build directory is used to house all the build files and assets for your application. 

The structure is:

* bin - Output directory
* darwin - macOS specific files
* linux - Linux specific files
* windows - Windows specific files

## Mac

The `darwin` directory holds files specific to Mac builds.
These may be customised and used as part of the build. To return these files to the default state, simply delete them
and
build with `wails build`.

The directory contains the following files:

- `Info.plist` - the main plist file used for Mac builds. It is used when building using `wails build`.
- `Info.dev.plist` - same as the main plist file but used when building using `wails dev`.

## Linux

The `linux` directory holds files used when packaging/distributing the Linux binary.

- `sessman.desktop` - FreeDesktop `.desktop` entry for application menus / AppImage / deb packaging.
  Install alongside the binary (e.g. `/usr/share/applications/`) and place an icon named `sessman`
  under the hicolor theme (or point `Icon=` at an absolute path).

Build on Linux (or WSL) with:

```bash
./build/linux/build-wsl.sh
# or:
wails build -platform linux/amd64 -tags webkit2_41
```

Use `-tags webkit2_40` only for older distros that still ship WebKit2GTK ABI 4.0
(Debian 11, Ubuntu 20.04, RHEL 8/9). Runtime libraries (`libgtk-3` + WebKit2GTK) must be
installed on the build and target machines — see https://wails.io/docs/guides/linux-distro-support/

Debian/Ubuntu 22.04+ (or WSL) one-time deps:

```bash
sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

## Windows

The `windows` directory contains the manifest and rc files used when building with `wails build`.
These may be customised for your application. To return these files to the default state, simply delete them and
build with `wails build`.

- `icon.ico` - The icon used for the application. This is used when building using `wails build`. If you wish to
  use a different icon, simply replace this file with your own. If it is missing, a new `icon.ico` file
  will be created using the `appicon.png` file in the build directory.
- `installer/*` - The files used to create the Windows installer. These are used when building using `wails build`.
- `info.json` - Application details used for Windows builds. The data here will be used by the Windows installer,
  as well as the application itself (right click the exe -> properties -> details)
- `wails.exe.manifest` - The main application manifest file.