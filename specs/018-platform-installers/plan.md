# 018 — Platform Installers: Implementation Plan

**Spec:** `specs/018-platform-installers/spec.md`
**Branch:** `feat/018-platform-installers`

---

## Pre-Implementation Gates

### DAG Gate
The `packaging/` directory is a build-time concern — it contains shell scripts, templates, and manifests that run outside the Go module. No new Go packages are created. No changes to the internal package dependency graph. The only Go code touched is in `Makefile` (new targets) and `.github/workflows/` (CI pipelines). The existing release workflow is extended, not replaced.

### Interface Gate
No new interfaces required. Installers package existing binaries (`sigild`, `sigilctl`, `sigil-app`). The installer scripts invoke `sigild init` on first launch — the same init flow all users run today. No new socket methods, no new internal APIs.

### Privacy Gate
Installers make zero network calls beyond the initial download. No telemetry, no update checks, no analytics. The installer is a file-placement mechanism. Installation does not create or modify any data stores. No PRIVACY.md update needed.

### Simplicity Gate
Each platform uses its native, well-documented packaging format — no custom installer frameworks. Build scripts are shell scripts (macOS/Linux) or PowerShell (Windows) that invoke standard tools (`create-dmg`, `dpkg-deb`, `rpmbuild`, WiX). Each script is independently runnable and testable. The CI workflow orchestrates them but does not replace them.

---

## Technical Design

### macOS: .dmg with Universal Binary

#### Universal Binary Creation

Go cross-compiles to `darwin/amd64` and `darwin/arm64` separately. The `lipo` tool (ships with Xcode CLI tools on macOS runners) combines them:

```bash
# Build both architectures
GOOS=darwin GOARCH=amd64 go build -o sigild-amd64 ./cmd/sigild/
GOOS=darwin GOARCH=arm64 go build -o sigild-arm64 ./cmd/sigild/

# Combine into universal binary
lipo -create -output sigild sigild-amd64 sigild-arm64

# Verify
lipo -info sigild  # "Architectures in the fat file: sigild are: x86_64 arm64"
```

Repeat for `sigilctl` and `sigil-app`. Note: `sigil-app` requires Wails build, which produces platform-specific binaries — the Wails build step runs twice (once per architecture) and `lipo` combines the results.

#### .app Bundle Assembly

The `create-app-bundle.sh` script assembles the standard macOS `.app` directory structure:

```bash
APP="Sigil.app"
mkdir -p "${APP}/Contents/MacOS"
mkdir -p "${APP}/Contents/Resources"

cp sigild sigilctl sigil-app "${APP}/Contents/MacOS/"
cp packaging/macos/Info.plist "${APP}/Contents/"
cp packaging/macos/sigil.icns "${APP}/Contents/Resources/"

# Info.plist key values:
# CFBundleIdentifier: tech.sigil.app
# CFBundleExecutable: sigil-app
# CFBundleName: Sigil
# CFBundleVersion: ${VERSION}
# LSMinimumSystemVersion: 12.0
# LSUIElement: true  (hides from Dock — tray-only app)
```

`LSUIElement: true` is critical — it makes the app a tray-only application that does not appear in the Dock or application switcher, matching the tray app behavior from spec 016.

#### .dmg Creation

Uses `create-dmg` (npm package, MIT license) for the polished drag-to-Applications experience:

```bash
npx create-dmg \
  --volname "Sigil" \
  --volicon "packaging/macos/sigil.icns" \
  --background "packaging/macos/dmg-background.png" \
  --window-pos 200 120 \
  --window-size 660 400 \
  --icon-size 80 \
  --icon "Sigil.app" 180 170 \
  --app-drop-link 480 170 \
  "Sigil-${VERSION}-universal.dmg" \
  "Sigil.app"
```

#### Code Signing and Notarization (SHOULD)

When `APPLE_DEVELOPER_ID` and related secrets are available in CI:

```bash
# Sign the .app bundle
codesign --deep --force --verify --verbose \
  --sign "Developer ID Application: ${APPLE_DEVELOPER_ID}" \
  --options runtime \
  --entitlements packaging/macos/entitlements.plist \
  "Sigil.app"

# Create signed .dmg (after create-dmg)
codesign --force --sign "Developer ID Application: ${APPLE_DEVELOPER_ID}" \
  "Sigil-${VERSION}-universal.dmg"

# Notarize
xcrun notarytool submit "Sigil-${VERSION}-universal.dmg" \
  --apple-id "${APPLE_ID}" \
  --team-id "${APPLE_TEAM_ID}" \
  --password "${APPLE_APP_PASSWORD}" \
  --wait

# Staple the ticket
xcrun stapler staple "Sigil-${VERSION}-universal.dmg"
```

The build scripts MUST detect missing credentials and skip signing gracefully — an unsigned `.dmg` is still produced and functional (with Gatekeeper warning).

#### Homebrew Cask

The cask formula downloads the `.dmg` from GitHub Releases:

```ruby
cask "sigil" do
  version "0.9.0"
  sha256 "abc123..."

  url "https://github.com/wambozi/sigil/releases/download/v#{version}/Sigil-#{version}-universal.dmg"
  name "Sigil"
  desc "Self-tuning intelligence layer for software engineers"
  homepage "https://github.com/wambozi/sigil"

  app "Sigil.app"

  binary "#{appdir}/Sigil.app/Contents/MacOS/sigild"
  binary "#{appdir}/Sigil.app/Contents/MacOS/sigilctl"

  postflight do
    system_command "/bin/ln", args: ["-sf",
      "#{appdir}/Sigil.app/Contents/MacOS/sigild", "/usr/local/bin/sigild"]
    system_command "/bin/ln", args: ["-sf",
      "#{appdir}/Sigil.app/Contents/MacOS/sigilctl", "/usr/local/bin/sigilctl"]
  end

  uninstall quit: "tech.sigil.app"
  zap trash: ["~/Library/Application Support/sigil", "~/.config/sigil"]
end
```

The existing Homebrew formula (builds from source, CLI-only) coexists with the cask (downloads `.dmg`, includes GUI). Users choose: `brew install sigil` (CLI) or `brew install --cask sigil` (GUI).

### Windows: .msi via WiX Toolset v4

#### WiX Installer Definition

The `sigil.wxs` file defines the complete installer:

```xml
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
  <Package Name="Sigil"
           Manufacturer="Sigil"
           Version="$(var.Version)"
           UpgradeCode="GUID-HERE">

    <MajorUpgrade DowngradeErrorMessage="A newer version is already installed." />

    <!-- Install directory -->
    <StandardDirectory Id="ProgramFiles6432Folder">
      <Directory Id="INSTALLFOLDER" Name="Sigil">
        <Component Id="Binaries">
          <File Source="sigild.exe" />
          <File Source="sigilctl.exe" />
          <File Source="sigil-app.exe" />

          <!-- Add to PATH -->
          <Environment Id="PATH" Name="PATH" Value="[INSTALLFOLDER]"
                       Permanent="no" Part="last" Action="set" System="no" />
        </Component>

        <Component Id="ServiceRegistration">
          <!-- Register sigild as a service -->
          <ServiceInstall Id="SigildService"
                          Name="sigild"
                          DisplayName="Sigil Daemon"
                          Description="Sigil workflow intelligence daemon"
                          Start="auto"
                          Type="ownProcess"
                          ErrorControl="normal"
                          Arguments="--service" />
          <ServiceControl Id="StartSigild" Name="sigild" Start="install" Stop="both"
                          Remove="uninstall" Wait="yes" />
        </Component>

        <Component Id="AutoStart">
          <!-- sigil-app auto-start on login -->
          <RegistryValue Root="HKCU"
                         Key="Software\Microsoft\Windows\CurrentVersion\Run"
                         Name="SigilApp"
                         Type="string"
                         Value="[INSTALLFOLDER]sigil-app.exe" />
        </Component>
      </Directory>
    </StandardDirectory>

    <!-- Start Menu shortcuts -->
    <StandardDirectory Id="ProgramMenuFolder">
      <Directory Id="SigilMenuFolder" Name="Sigil">
        <Component Id="StartMenuShortcuts">
          <Shortcut Id="SigilAppShortcut"
                    Name="Sigil"
                    Target="[INSTALLFOLDER]sigil-app.exe"
                    WorkingDirectory="INSTALLFOLDER"
                    Icon="sigil.ico" />
          <RemoveFolder Id="RemoveSigilMenuFolder" On="uninstall" />
          <RegistryValue Root="HKCU" Key="Software\Sigil" Name="installed"
                         Type="integer" Value="1" KeyPath="yes" />
        </Component>
      </Directory>
    </StandardDirectory>

    <Feature Id="Complete" Level="1">
      <ComponentRef Id="Binaries" />
      <ComponentRef Id="ServiceRegistration" />
      <ComponentRef Id="AutoStart" />
      <ComponentRef Id="StartMenuShortcuts" />
    </Feature>
  </Package>
</Wix>
```

#### MSI Build

The `build-msi.ps1` script runs on a Windows CI runner:

```powershell
# Install WiX v4 as .NET tool
dotnet tool install --global wix

# Build MSI
wix build `
  -d Version=$env:VERSION `
  -o "Sigil-${env:VERSION}-x64.msi" `
  packaging/windows/sigil.wxs
```

#### winget Manifest

Three YAML files in the standard `winget-pkgs` format:

```yaml
# Sigil.Sigil.yaml
PackageIdentifier: Sigil.Sigil
PackageVersion: "0.9.0"
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
```

```yaml
# Sigil.Sigil.installer.yaml
PackageIdentifier: Sigil.Sigil
PackageVersion: "0.9.0"
Installers:
  - Architecture: x64
    InstallerType: msi
    InstallerUrl: https://github.com/wambozi/sigil/releases/download/v0.9.0/Sigil-0.9.0-x64.msi
    InstallerSha256: abc123...
ManifestType: installer
ManifestVersion: 1.6.0
```

The `winget` manifest is submitted via PR to `microsoft/winget-pkgs`. A CI workflow automates this on new releases.

#### Scoop Manifest

```json
{
  "version": "0.9.0",
  "description": "Self-tuning intelligence layer for software engineers",
  "homepage": "https://github.com/wambozi/sigil",
  "license": "BSL-1.1",
  "architecture": {
    "64bit": {
      "url": "https://github.com/wambozi/sigil/releases/download/v0.9.0/Sigil-0.9.0-x64.msi",
      "hash": "abc123..."
    }
  },
  "installer": {
    "script": "Start-Process msiexec -ArgumentList '/i', \"$dir\\Sigil-0.9.0-x64.msi\", '/quiet' -Wait"
  },
  "uninstaller": {
    "script": "Start-Process msiexec -ArgumentList '/x', \"$dir\\Sigil-0.9.0-x64.msi\", '/quiet' -Wait"
  }
}
```

### Linux: AppImage, .deb, .rpm

#### AppImage

The AppImage bundles all three binaries in a self-contained executable:

```bash
# AppDir structure
mkdir -p Sigil.AppDir/usr/bin
mkdir -p Sigil.AppDir/usr/share/applications
mkdir -p Sigil.AppDir/usr/share/icons/hicolor/256x256/apps

cp sigild sigilctl sigil-app Sigil.AppDir/usr/bin/
cp packaging/linux/sigil.desktop Sigil.AppDir/usr/share/applications/
cp assets/icon.png Sigil.AppDir/usr/share/icons/hicolor/256x256/apps/sigil.png
cp packaging/linux/appimage/AppRun Sigil.AppDir/
cp packaging/linux/sigil.desktop Sigil.AppDir/
cp assets/icon.png Sigil.AppDir/sigil.png

# Build AppImage
ARCH=x86_64 appimagetool Sigil.AppDir "Sigil-${VERSION}-x86_64.AppImage"
```

The `AppRun` script is the entry point that `appimagetool` invokes:

```bash
#!/bin/bash
HERE="$(dirname "$(readlink -f "$0")")"

case "$(basename "$0")" in
  sigild)   exec "${HERE}/usr/bin/sigild" "$@" ;;
  sigilctl) exec "${HERE}/usr/bin/sigilctl" "$@" ;;
  *)        exec "${HERE}/usr/bin/sigil-app" "$@" ;;
esac
```

By default the AppImage launches `sigil-app`. Users can create symlinks named `sigild` or `sigilctl` pointing to the AppImage to invoke those binaries directly.

#### .deb Package

Built with `dpkg-deb` from a staged directory:

```bash
PKG="sigil_${VERSION}_${ARCH}"
mkdir -p "${PKG}/DEBIAN"
mkdir -p "${PKG}/usr/bin"
mkdir -p "${PKG}/usr/lib/systemd/user"
mkdir -p "${PKG}/usr/share/applications"
mkdir -p "${PKG}/usr/share/icons/hicolor/256x256/apps"
mkdir -p "${PKG}/etc/sigil"

# Binaries
cp sigild sigilctl sigil-app "${PKG}/usr/bin/"

# Service
cp packaging/linux/sigild.service "${PKG}/usr/lib/systemd/user/"

# Desktop entry
cp packaging/linux/sigil.desktop "${PKG}/usr/share/applications/"

# Icon
cp assets/icon.png "${PKG}/usr/share/icons/hicolor/256x256/apps/sigil.png"

# Config
cp config.example.toml "${PKG}/etc/sigil/config.example.toml"

# Control files
envsubst < packaging/linux/deb/control > "${PKG}/DEBIAN/control"
cp packaging/linux/deb/postinst "${PKG}/DEBIAN/"
cp packaging/linux/deb/prerm "${PKG}/DEBIAN/"
cp packaging/linux/deb/conffiles "${PKG}/DEBIAN/"
chmod 755 "${PKG}/DEBIAN/postinst" "${PKG}/DEBIAN/prerm"

dpkg-deb --build "${PKG}"
```

The `control` file:

```
Package: sigil
Version: ${VERSION}
Architecture: ${ARCH}
Maintainer: Alec Feeman <alec@sigil.tech>
Description: Self-tuning intelligence layer for software engineers
 Sigil runs as a background daemon observing workflow signals,
 detecting patterns, and surfacing suggestions as desktop notifications.
Section: utils
Priority: optional
```

The `postinst` script:

```bash
#!/bin/sh
set -e
# Reload systemd user units
if command -v systemctl >/dev/null 2>&1; then
  systemctl --user daemon-reload || true
  echo "To start sigild: systemctl --user enable --now sigild"
fi
```

#### .rpm Package

Built with `rpmbuild` from a spec file:

```spec
Name:    sigil
Version: %{version}
Release: 1
Summary: Self-tuning intelligence layer for software engineers
License: BSL-1.1
URL:     https://github.com/wambozi/sigil

%description
Sigil runs as a background daemon observing workflow signals,
detecting patterns, and surfacing suggestions as desktop notifications.

%install
mkdir -p %{buildroot}/usr/bin
mkdir -p %{buildroot}/usr/lib/systemd/user
mkdir -p %{buildroot}/usr/share/applications
mkdir -p %{buildroot}/usr/share/icons/hicolor/256x256/apps
mkdir -p %{buildroot}/etc/sigil

install -m 755 sigild %{buildroot}/usr/bin/sigild
install -m 755 sigilctl %{buildroot}/usr/bin/sigilctl
install -m 755 sigil-app %{buildroot}/usr/bin/sigil-app
install -m 644 sigild.service %{buildroot}/usr/lib/systemd/user/sigild.service
install -m 644 sigil.desktop %{buildroot}/usr/share/applications/sigil.desktop
install -m 644 sigil.png %{buildroot}/usr/share/icons/hicolor/256x256/apps/sigil.png
install -m 644 config.example.toml %{buildroot}/etc/sigil/config.example.toml

%files
/usr/bin/sigild
/usr/bin/sigilctl
/usr/bin/sigil-app
/usr/lib/systemd/user/sigild.service
/usr/share/applications/sigil.desktop
/usr/share/icons/hicolor/256x256/apps/sigil.png
%config(noreplace) /etc/sigil/config.example.toml

%post
systemctl --user daemon-reload || true

%preun
systemctl --user disable --now sigild || true
```

### CI/CD Workflow Extension

The release workflow is extended with platform-specific jobs that depend on the existing binary build:

```yaml
jobs:
  build:        # existing job — produces bare binaries
  package-macos:
    needs: build
    runs-on: macos-latest
    # Builds universal binary, .app bundle, .dmg
  package-windows:
    needs: build
    runs-on: windows-latest
    # Builds .msi via WiX
  package-linux:
    needs: build
    runs-on: ubuntu-latest
    # Builds AppImage, .deb, .rpm
  release:
    needs: [build, package-macos, package-windows, package-linux]
    # Uploads all artifacts to GitHub Release
```

Each packaging job downloads the bare binaries from the build job's artifacts, then packages them. This avoids rebuilding Go binaries per platform.

**Exception:** macOS universal binaries require both `darwin/amd64` and `darwin/arm64` binaries, which the existing build job already produces. The `package-macos` job downloads both and runs `lipo`.

**Exception:** The Windows `.msi` needs Windows binaries. The existing build matrix must be extended to include `windows/amd64` (currently missing from the release workflow).

---

## Implementation Phases

### Phase 1: Packaging Directory Scaffold

Create the `packaging/` directory structure with placeholder files and Makefile targets. Establish the build script pattern that all subsequent phases follow.

**Files created:**
- `packaging/macos/` — empty directory with `create-dmg.sh`, `create-app-bundle.sh` stubs
- `packaging/windows/` — empty directory with `build-msi.ps1` stub
- `packaging/linux/` — empty directory with `build-appimage.sh`, `build-deb.sh`, `build-rpm.sh` stubs
- `packaging/scripts/build-all.sh` — orchestrator stub
- `Makefile` — add `package-macos`, `package-windows`, `package-linux`, `package-all` targets

**Verification:** `make package-all` runs and prints "not yet implemented" for each platform.

### Phase 2: macOS .app Bundle + Universal Binary

Build the core macOS packaging: universal binary creation via `lipo` and `.app` bundle assembly. No `.dmg` yet — just the `.app`.

**Key decisions:**
- `LSUIElement: true` — tray-only app, no Dock icon
- `CFBundleIdentifier: tech.sigil.app`
- Minimum macOS version: 12.0 (Monterey) — oldest supported by Go 1.24

**Files created:**
- `packaging/macos/Info.plist`
- `packaging/macos/entitlements.plist`
- `packaging/macos/create-app-bundle.sh`

**Verification:** `./packaging/macos/create-app-bundle.sh` produces `Sigil.app/` with universal binaries. `lipo -info Sigil.app/Contents/MacOS/sigild` shows both architectures.

### Phase 3: macOS .dmg Creation

Wrap the `.app` bundle in a `.dmg` with drag-to-Applications layout.

**Files created:**
- `packaging/macos/create-dmg.sh`
- `packaging/macos/dmg-background.png`

**Verification:** `./packaging/macos/create-dmg.sh` produces `Sigil-VERSION-universal.dmg`. Opening the `.dmg` shows the app icon and Applications alias.

### Phase 4: macOS Code Signing + Notarization (SHOULD)

Add optional code signing and notarization. The build scripts detect available credentials and sign when possible, skip when not.

**Files created:**
- `packaging/scripts/sign-and-notarize.sh`

**Files modified:**
- `packaging/macos/create-app-bundle.sh` — call signing if credentials available
- `packaging/macos/create-dmg.sh` — call signing if credentials available

**Verification:** With test credentials, `.app` passes `codesign --verify`. Without credentials, build completes with warning but no failure.

### Phase 5: Windows .msi Installer

Build the Windows installer using WiX Toolset v4.

**Files created:**
- `packaging/windows/sigil.wxs`
- `packaging/windows/build-msi.ps1`
- `packaging/windows/sigil.ico`
- `packaging/windows/banner.bmp`
- `packaging/windows/dialog.bmp`

**Verification:** On a Windows runner or VM, `./packaging/windows/build-msi.ps1` produces `Sigil-VERSION-x64.msi`. Running the MSI installs to Program Files, adds to PATH, registers service, creates Start Menu shortcut. Uninstall removes all.

### Phase 6: Linux AppImage

Build the AppImage for distribution-agnostic Linux installation.

**Files created:**
- `packaging/linux/build-appimage.sh`
- `packaging/linux/appimage/AppRun`
- `packaging/linux/appimage/sigil.appdata.xml`
- `packaging/linux/sigil.desktop`

**Verification:** `./packaging/linux/build-appimage.sh` produces `Sigil-VERSION-x86_64.AppImage`. Running the AppImage on Ubuntu 20.04+ launches `sigil-app`. Symlink trick for `sigild`/`sigilctl` works.

### Phase 7: Linux .deb and .rpm Packages

Build native Linux packages for Debian/Ubuntu and Fedora/RHEL.

**Files created:**
- `packaging/linux/build-deb.sh`
- `packaging/linux/build-rpm.sh`
- `packaging/linux/deb/control`, `postinst`, `prerm`, `conffiles`
- `packaging/linux/rpm/sigil.spec`
- `packaging/linux/sigild.service` (copied from `deploy/sigild.service`)

**Verification:** `dpkg-deb --build` produces installable `.deb`. `rpmbuild` produces installable `.rpm`. Installing either places binaries in `/usr/bin/` and systemd service in correct location.

### Phase 8: CI/CD Release Workflow

Extend the existing release workflow to build all installer artifacts on tagged releases.

**Files modified:**
- `.github/workflows/release.yml` — add Windows to build matrix, add `package-macos`, `package-windows`, `package-linux` jobs, extend release step to upload installer artifacts

**Files created:**
- `.github/workflows/homebrew-cask.yml` — auto-update Homebrew cask on release

**Verification:** Push a test tag. CI produces all artifacts: bare binaries (existing), `.dmg`, `.msi`, AppImage, `.deb`, `.rpm`, checksums. All uploaded to GitHub Release.

### Phase 9: Package Manager Manifests

Create and submit manifests for Homebrew cask, winget, and Scoop.

**Files created:**
- `packaging/macos/homebrew-cask.rb`
- `packaging/windows/winget/` (3 YAML files)
- `packaging/windows/scoop/sigil.json`

**Verification:** `brew install --cask sigil` installs from `.dmg`. `winget` and `scoop` manifests pass validation. Auto-update CI workflow updates manifests on new releases.

### Phase 10: Update install.sh + Documentation

Update the existing `install.sh` to install `sigil-app` alongside the daemon and CLI. Update project documentation.

**Files modified:**
- `scripts/install.sh` — add `sigil-app` download and install
- `CLAUDE.md` — add packaging commands
- `CONTRIBUTING.md` — add packaging build instructions
- `.github/pull_request_template.md` — add installer CI check if applicable

**Verification:** `install.sh` installs all three binaries. Documentation accurately describes the new build targets and CI pipeline.

---

## Testing Strategy

### Build Tests (CI)

| Test | What it verifies |
|------|-----------------|
| `make package-macos` on `macos-latest` | .dmg produces with valid .app bundle and universal binaries |
| `make package-windows` on `windows-latest` | .msi produces via WiX, installs silently |
| `make package-linux` on `ubuntu-latest` | AppImage, .deb, and .rpm all produce |
| Checksum verification | All artifacts have SHA256 checksums in the release |

### Installation Smoke Tests (CI)

| Test | Platform | What it verifies |
|------|----------|-----------------|
| `.msi` silent install + PATH check | Windows | `sigild --version` works after install |
| `.deb` install + service check | Ubuntu | `dpkg -i` succeeds, `systemctl --user status sigild` shows unit |
| `.rpm` install + service check | Fedora (container) | `rpm -i` succeeds, service file exists |
| AppImage execution | Ubuntu | `./Sigil.AppImage --help` runs without error |
| `install.sh` dry run | Linux | Script runs, downloads correct URLs, installs three binaries |

### Manual Verification

| Test | Platform | What it verifies |
|------|----------|-----------------|
| `.dmg` drag-to-Applications | macOS | Full install experience: open dmg, drag, launch from Launchpad |
| `.msi` GUI install | Windows | Full install wizard, Start Menu shortcut, service auto-starts |
| AppImage double-click | Linux (GNOME) | File manager "Run" launches `sigil-app` |
| Uninstall + clean check | All | No files, services, or registry entries remain after uninstall |
| `brew install --cask sigil` | macOS | End-to-end cask install from tap |
| Unsigned .dmg Gatekeeper | macOS | Gatekeeper warning appears, right-click "Open" bypasses it |

### Performance Tests

| Metric | Target | How to measure |
|--------|--------|---------------|
| `.dmg` size | < 80MB (3 universal binaries) | `ls -la *.dmg` |
| `.msi` size | < 40MB | `ls -la *.msi` |
| AppImage size | < 40MB | `ls -la *.AppImage` |
| `.deb` size | < 40MB | `ls -la *.deb` |
| CI build time (full release) | < 30 minutes | GitHub Actions run duration |
| `install.sh` execution time | < 60 seconds | Manual timing on fast network |
