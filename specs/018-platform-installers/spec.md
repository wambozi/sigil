# 018 — Platform Installers

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-03-27

---

## Problem

Sigil currently distributes as bare binaries on GitHub Releases (per-platform `sigild-*` and `sigilctl-*` files) with a Linux-only `install.sh` script. This creates three adoption barriers:

1. **No macOS installer:** macOS users must build from source or manually download and place binaries. There is no `.dmg`, no Homebrew cask (the existing Homebrew formula builds from source but does not install `sigil-app`), and no universal binary for Apple Silicon + Intel.
2. **No Windows installer:** Windows users have no installation path at all. No `.msi`, no `winget`, no `scoop` manifest. There is no mechanism to add `sigild` to PATH, register it as a service, or configure auto-start.
3. **No GUI-friendly Linux installer:** The `install.sh` script works for terminal users, but there is no AppImage, `.deb`, `.rpm`, or Flatpak for users who expect a double-click install experience. The tray app (spec 016) specifically targets users who prefer GUI over terminal.

These gaps mean that the tray app (spec 016) has no delivery vehicle for non-technical users on any platform. Business users who expect "download, double-click, run" cannot adopt Sigil today.

## Solution

Native platform installers that bundle `sigild`, `sigilctl`, and `sigil-app` into a single installable artifact per platform. Each installer follows platform conventions: `.dmg` on macOS, `.msi` on Windows, AppImage/`.deb`/`.rpm` on Linux. Package manager manifests (`brew cask`, `winget`, `scoop`) provide the technical-user path alongside the GUI installer path.

All installers are built in CI via GitHub Actions, attached to GitHub Releases as artifacts, and verified with SHA256 checksums. The existing `install.sh` script remains as the lightweight Linux path for technical users.

### Prerequisites

This spec depends on:
- **Spec 016 (Sigil Tray App):** The `sigil-app` binary must exist to be bundled. Installers without the tray app are still useful (sigild + sigilctl only), but the full value requires spec 016.
- **Spec 017 (Windows Daemon Support):** sigild must build and run on Windows before a Windows installer is meaningful.

## Requirements

### General — MUST

1. All installers MUST bundle `sigild`, `sigilctl`, and `sigil-app` into a single artifact
2. All installers MUST place binaries in platform-conventional locations
3. All installers MUST be reproducible from CI (GitHub Actions) — no manual build steps
4. All release artifacts MUST include SHA256 checksums (extend existing `checksums-*.txt` pattern)
5. All installers MUST support clean uninstallation that removes all installed files
6. The `packaging/` directory MUST contain all installer build scripts, templates, and manifests
7. The existing `scripts/install.sh` MUST remain functional and be updated to also install `sigil-app` when available

### General — MUST NOT

8. Installers MUST NOT make network calls beyond the initial download — no phone-home, no telemetry, no update checks
9. Installers MUST NOT collect or transmit any user data during installation
10. Installers MUST NOT require root/administrator privileges for per-user installation (system-wide installation MAY require elevation)
11. Installers MUST NOT modify system configuration beyond what is necessary for Sigil to function (PATH, service registration, auto-start)
12. Installers MUST NOT bundle or download third-party runtimes without explicit user consent (e.g., WebView2 on Windows)

### macOS — MUST

13. A `.dmg` disk image MUST be produced with a drag-to-Applications layout (app icon + Applications alias)
14. The `.dmg` MUST contain a macOS `.app` bundle that wraps `sigil-app` with proper `Info.plist`, icon, and entitlements
15. A universal binary (arm64 + amd64 via `lipo`) MUST be produced so one download works on both Apple Silicon and Intel Macs
16. The `.app` bundle MUST include `sigild` and `sigilctl` in `Contents/MacOS/` alongside `sigil-app`
17. A post-install step (or first-launch flow) MUST offer to add `sigild` and `sigilctl` to PATH via symlinks in `/usr/local/bin/` or shell profile modification
18. A Homebrew cask formula MUST be maintained that installs from the `.dmg` release artifact

### macOS — SHOULD

19. The `.dmg` and `.app` bundle SHOULD be code-signed with a Developer ID certificate
20. The `.app` bundle SHOULD be notarized with Apple's notarization service so Gatekeeper does not block it
21. The Homebrew cask SHOULD auto-update when a new GitHub Release is published (via CI workflow)

### Windows — MUST

22. An `.msi` installer MUST be produced using WiX Toolset v4 (open-source, CI-friendly)
23. The `.msi` MUST install `sigild.exe`, `sigilctl.exe`, and `sigil-app.exe` to `%PROGRAMFILES%\Sigil\` (system-wide) or `%LOCALAPPDATA%\Sigil\` (per-user)
24. The `.msi` MUST add the install directory to the user's PATH
25. The `.msi` MUST register `sigild` as a Windows service via `sc create` or Task Scheduler, configured for auto-start on login
26. The `.msi` MUST register `sigil-app` for auto-start via the `Run` registry key
27. The `.msi` MUST support silent installation via `msiexec /quiet`
28. The `.msi` MUST create Start Menu shortcuts for `sigil-app` and an uninstaller entry in "Add/Remove Programs"
29. A `winget` manifest MUST be maintained in the `microsoft/winget-pkgs` repository format
30. A Scoop manifest MUST be maintained for technical users who prefer Scoop

### Windows — SHOULD

31. The `.msi` SHOULD detect whether WebView2 runtime is installed and offer to download it if missing (required by Wails on Windows 10)
32. The installer SHOULD be Authenticode-signed with a code signing certificate
33. The `winget` and Scoop manifests SHOULD auto-update when a new GitHub Release is published

### Linux — MUST

34. An AppImage MUST be produced that bundles all three binaries and runs on any Linux distribution without installation
35. A `.deb` package MUST be produced for Debian/Ubuntu users, installable via `dpkg -i` or `apt install ./sigil.deb`
36. An `.rpm` package MUST be produced for Fedora/RHEL users, installable via `rpm -i` or `dnf install ./sigil.rpm`
37. The `.deb` and `.rpm` MUST install binaries to `/usr/bin/`, config to `/etc/sigil/`, and systemd service file to `/usr/lib/systemd/user/`
38. The `.deb` and `.rpm` MUST include a systemd user service file for `sigild` (adapt existing `deploy/sigild.service`)
39. The AppImage MUST be self-contained — no external dependencies beyond glibc 2.17+
40. The existing `scripts/install.sh` MUST be updated to also download and install `sigil-app` alongside `sigild` and `sigilctl`

### Linux — SHOULD

41. A Flatpak manifest SHOULD be produced for distribution via Flathub
42. The `.deb` package SHOULD include a `.desktop` file so `sigil-app` appears in the application menu
43. The `.rpm` package SHOULD include the same `.desktop` file

### CI/CD — MUST

44. The release workflow (`.github/workflows/release.yml`) MUST be extended to produce all installer artifacts alongside the existing bare binaries
45. Each artifact MUST have its SHA256 checksum included in the release checksums file
46. The CI workflow MUST use GitHub-hosted runners — no self-hosted infrastructure required
47. macOS builds MUST use `macos-latest` runners; Windows builds MUST use `windows-latest` runners
48. The release workflow MUST produce artifacts for: `darwin/universal`, `windows/amd64`, `linux/amd64`, `linux/arm64`

### CI/CD — SHOULD

49. The CI workflow SHOULD cache build dependencies (Go modules, npm packages, WiX toolset) for faster builds
50. The CI workflow SHOULD run installer smoke tests (install, verify binary exists, uninstall) where feasible

## Success Criteria

- [ ] `make package-macos` produces a `.dmg` with drag-to-Applications layout
- [ ] `.dmg` contains universal binary (arm64 + amd64) `.app` bundle
- [ ] `brew install --cask sigil` installs from the `.dmg`
- [ ] `make package-windows` produces an `.msi` installer
- [ ] `.msi` installs to Program Files, adds to PATH, registers service, creates Start Menu shortcut
- [ ] `winget install sigil` and `scoop install sigil` work
- [ ] `make package-linux` produces AppImage, `.deb`, and `.rpm`
- [ ] AppImage runs on Ubuntu 20.04+ without installation
- [ ] `dpkg -i sigil.deb` installs and enables systemd user service
- [ ] `rpm -i sigil.rpm` installs and enables systemd user service
- [ ] GitHub Release for a tagged version includes all installer artifacts + checksums
- [ ] No installer makes network calls beyond the initial download
- [ ] All installers support clean uninstallation
- [ ] `scripts/install.sh` installs `sigil-app` alongside `sigild` and `sigilctl`

## Entities & Data

- **New directory:** `packaging/` — all installer build scripts, templates, and manifests
- **Modified workflow:** `.github/workflows/release.yml` — extended to build installers
- **New workflow:** `.github/workflows/homebrew-cask.yml` — auto-update Homebrew cask on release
- **Modified script:** `scripts/install.sh` — updated to include `sigil-app`
- **Makefile targets:** `package-macos`, `package-windows`, `package-linux`, `package-all`
- **Build dependencies (CI only):** WiX Toolset v4, `create-dmg`, `appimagetool`, `dpkg-deb`, `rpmbuild`, `lipo`

## Project Structure

```
packaging/
├── macos/
│   ├── create-dmg.sh              # Script to build .dmg from .app bundle
│   ├── create-app-bundle.sh       # Script to assemble .app from binaries
│   ├── Info.plist                  # macOS app bundle metadata
│   ├── entitlements.plist          # Code signing entitlements
│   ├── sigil.icns                  # macOS icon file (from assets/icon.png)
│   ├── dmg-background.png         # .dmg window background image
│   └── homebrew-cask.rb           # Homebrew cask formula template
├── windows/
│   ├── sigil.wxs                  # WiX v4 installer XML definition
│   ├── build-msi.ps1             # PowerShell script to build .msi
│   ├── sigil.ico                  # Windows icon file
│   ├── banner.bmp                 # WiX installer banner image
│   ├── dialog.bmp                 # WiX installer dialog background
│   ├── winget/
│   │   ├── Sigil.Sigil.installer.yaml
│   │   ├── Sigil.Sigil.locale.en-US.yaml
│   │   └── Sigil.Sigil.yaml
│   └── scoop/
│       └── sigil.json             # Scoop manifest
├── linux/
│   ├── build-appimage.sh          # Script to build AppImage
│   ├── build-deb.sh               # Script to build .deb package
│   ├── build-rpm.sh               # Script to build .rpm package
│   ├── sigil.desktop              # XDG desktop entry for sigil-app
│   ├── sigild.service             # systemd user service (copy from deploy/)
│   ├── appimage/
│   │   ├── AppRun                 # AppImage entry point
│   │   └── sigil.appdata.xml      # AppStream metadata
│   ├── deb/
│   │   ├── control                # Debian package control file
│   │   ├── postinst               # Post-install script
│   │   ├── prerm                  # Pre-removal script
│   │   └── conffiles              # Config files list
│   └── rpm/
│       └── sigil.spec             # RPM spec file
└── scripts/
    ├── build-all.sh               # Orchestrator: build all platform packages
    └── sign-and-notarize.sh       # macOS code signing + notarization
```

## Constitution Alignment

- **Privacy-First:** Installers make zero network calls beyond the initial download. No telemetry, no update checks, no phone-home. The installer is a delivery vehicle, not a data collection point. No PRIVACY.md update needed.
- **Daemon-First:** The daemon is the core product. Installers are a distribution mechanism — they place binaries on disk and configure auto-start. No daemon logic lives in the installer.
- **Observable:** Installation is transparent — all files are placed in documented, platform-conventional locations. Uninstallation removes exactly what was installed. No hidden files or registry entries beyond what is documented.
- **Minimal Dependencies:** Build-time dependencies (WiX, `create-dmg`, `appimagetool`) run only in CI. The installed product has the same minimal runtime dependencies as the bare binaries.
- **Progressive Trust:** The installer configures `sigild` at notification level 2 (Ambient) by default — same as `sigild init`. The user escalates trust after installation, not during.

## Relationship to Other Components

| Component | Relationship |
|-----------|-------------|
| `sigild` | Bundled binary. Installer places it, configures auto-start, and optionally registers as a service. |
| `sigilctl` | Bundled binary. Installer places it on PATH. |
| `sigil-app` | Bundled binary. Installer places it, registers for auto-start, creates Start Menu/app-menu entry. |
| `install.sh` | Remains as lightweight Linux installer for technical users. Updated to also install `sigil-app`. |
| Homebrew formula | Existing formula builds from source (CLI-only). New cask installs from `.dmg` (includes GUI). Both coexist. |
| GitHub Releases | Installers are attached as additional artifacts alongside existing bare binaries. |
| `deploy/sigild.service` | Copied into Linux packages. The installer does not create a new service file — it reuses the existing one. |

## Platform-Specific Notes

### macOS

The `.app` bundle is the standard macOS distribution format. It wraps the three binaries in a directory structure that macOS treats as a single application:

```
Sigil.app/
├── Contents/
│   ├── Info.plist           # Bundle metadata (CFBundleIdentifier: tech.sigil.app)
│   ├── MacOS/
│   │   ├── sigil-app        # Main executable (universal binary)
│   │   ├── sigild           # Daemon (universal binary)
│   │   └── sigilctl         # CLI tool (universal binary)
│   ├── Resources/
│   │   └── sigil.icns       # Application icon
│   └── Frameworks/          # Empty unless Wails embeds WebKit framework
```

Universal binaries are created with `lipo -create -output sigild sigild-amd64 sigild-arm64`. This doubles the binary size (~34MB total for sigild) but eliminates the "which architecture?" question for users.

The `.dmg` uses `create-dmg` (Node.js tool, MIT license) for the drag-to-Applications layout with a custom background image. Alternative: `hdiutil` directly, but `create-dmg` handles icon positioning and background reliably.

**Code signing and notarization** require an Apple Developer ID certificate ($99/year). For MVP, unsigned distribution with Gatekeeper bypass instructions is acceptable. The build scripts MUST support signing when credentials are available but MUST NOT fail without them.

### Windows

WiX Toolset v4 is the standard open-source MSI authoring tool. It runs on Windows and is available as a .NET tool (`dotnet tool install wix`). The `.wxs` XML file defines the installer contents, registry entries, PATH modification, and service registration.

Key WiX components:
- **Directory structure:** `ProgramFiles\Sigil\` for system-wide, `LocalAppData\Sigil\` for per-user
- **PATH modification:** WiX `Environment` element appends install dir to user PATH
- **Service registration:** WiX `ServiceInstall` and `ServiceControl` elements for `sigild`
- **Auto-start:** Registry `Run` key for `sigil-app.exe`
- **Start Menu:** WiX `Shortcut` elements for `sigil-app` and uninstaller

The `.msi` build requires a Windows runner in CI. Cross-compilation from Linux is possible with `wixl` (msitools) but produces lower-quality MSIs. Using a native Windows runner ensures full WiX v4 compatibility.

### Linux

**AppImage** bundles the three binaries with a minimal runtime (AppRun entry script). It targets glibc 2.17+ compatibility, which covers Ubuntu 18.04+ and most modern distributions. Built with `appimagetool` from the AppImage project.

**`.deb` package** structure:
```
sigil_VERSION_ARCH/
├── DEBIAN/
│   ├── control              # Package metadata
│   ├── postinst             # Enable systemd user service
│   ├── prerm                # Disable systemd user service
│   └── conffiles            # /etc/sigil/config.toml
├── usr/bin/
│   ├── sigild
│   ├── sigilctl
│   └── sigil-app
├── usr/lib/systemd/user/
│   └── sigild.service
├── usr/share/applications/
│   └── sigil.desktop
├── usr/share/icons/hicolor/256x256/apps/
│   └── sigil.png
└── etc/sigil/
    └── config.example.toml
```

**`.rpm` package** follows the same layout but uses an RPM `.spec` file instead of `DEBIAN/control`.

**Flatpak** is a SHOULD item. It requires a Flatpak manifest and submission to Flathub. The main challenge is that sigild needs filesystem access (for file watching) and D-Bus access (for notifications), which requires Flatpak permissions that partially defeat sandboxing. Evaluated after the MUST items ship.

## Artifact Matrix

| Platform | Artifact | Contains | Install Method |
|----------|----------|----------|---------------|
| macOS | `Sigil-VERSION-universal.dmg` | sigild + sigilctl + sigil-app (universal) | Drag to Applications |
| macOS | Homebrew cask | Same as .dmg | `brew install --cask sigil` |
| Windows | `Sigil-VERSION-x64.msi` | sigild + sigilctl + sigil-app | Run installer |
| Windows | winget manifest | Points to .msi | `winget install Sigil.Sigil` |
| Windows | Scoop manifest | Points to .msi or zip | `scoop install sigil` |
| Linux | `Sigil-VERSION-x86_64.AppImage` | sigild + sigilctl + sigil-app | `chmod +x && ./` |
| Linux | `sigil_VERSION_amd64.deb` | sigild + sigilctl + sigil-app | `sudo dpkg -i` |
| Linux | `sigil_VERSION_amd64.rpm` | sigild + sigilctl + sigil-app | `sudo rpm -i` |
| Linux | `sigil_VERSION_arm64.deb` | sigild + sigilctl + sigil-app | `sudo dpkg -i` |
| Linux | `sigil_VERSION_arm64.rpm` | sigild + sigilctl + sigil-app | `sudo rpm -i` |
| Linux | `install.sh` | Downloads from release | `curl \| sh` |
