# 018 — Platform Installers: Tasks

**Branch:** `feat/018-platform-installers`

---

## Tasks

### Phase 1: Packaging Directory Scaffold

- [ ] **Task 1.1**: Create `packaging/` directory structure
  - Create all subdirectories: `macos/`, `windows/`, `windows/winget/`, `windows/scoop/`, `linux/`, `linux/appimage/`, `linux/deb/`, `linux/rpm/`, `scripts/`
  - Add `.gitkeep` to empty directories
  - Files: `packaging/` (entire tree)
  - Test: Directory structure matches spec project structure
  - Depends: none

- [ ] **Task 1.2**: Add Makefile packaging targets (stubs)
  - Add `package-macos`, `package-windows`, `package-linux`, `package-all` targets
  - Each target prints "not yet implemented" and exits 0 for now
  - Stub targets call into `packaging/<platform>/` scripts
  - Files: `Makefile`
  - Test: `make package-all` runs without error
  - Depends: none

- [ ] **Task 1.3**: Add `packaging/scripts/build-all.sh` orchestrator
  - Shell script that detects current OS and runs the appropriate platform build
  - Accepts `--version` flag for version injection
  - Validates prerequisites (Go, required tools) before proceeding
  - Files: `packaging/scripts/build-all.sh`
  - Test: Script runs, reports missing prerequisites gracefully
  - Depends: Task 1.1

**Phase 1 verification:** `make package-all` runs. Directory structure exists. Orchestrator script detects OS and reports prerequisites.

---

### Phase 2: macOS .app Bundle + Universal Binary

- [ ] **Task 2.1**: Create `Info.plist` for macOS app bundle
  - Set `CFBundleIdentifier: tech.sigil.app`, `CFBundleExecutable: sigil-app`
  - Set `LSUIElement: true` (tray-only, no Dock icon)
  - Set `LSMinimumSystemVersion: 12.0`
  - Include version placeholder `${VERSION}` for build-time substitution
  - Files: `packaging/macos/Info.plist`
  - Test: `plutil -lint packaging/macos/Info.plist` passes
  - Depends: none

- [ ] **Task 2.2**: Create `entitlements.plist` for code signing
  - Include `com.apple.security.cs.allow-unsigned-executable-memory` (required by Go binaries)
  - Include `com.apple.security.network.client` (for socket connections)
  - Include `com.apple.security.files.user-selected.read-write` (for file watching)
  - Files: `packaging/macos/entitlements.plist`
  - Test: `plutil -lint packaging/macos/entitlements.plist` passes
  - Depends: none

- [ ] **Task 2.3**: Create `sigil.icns` macOS icon file [P]
  - Convert `assets/icon.png` (1024x1024) to `.icns` format with all required sizes (16, 32, 64, 128, 256, 512, 1024)
  - Use `iconutil` or `sips` on macOS
  - Files: `packaging/macos/sigil.icns`
  - Test: `file packaging/macos/sigil.icns` reports valid icns
  - Depends: none

- [ ] **Task 2.4**: Implement `create-app-bundle.sh`
  - Accept `--version`, `--bin-dir` (directory containing pre-built binaries), and `--output` flags
  - Build universal binaries via `lipo -create` for each of `sigild`, `sigilctl`, `sigil-app`
  - Assemble `.app` directory structure: `Contents/MacOS/`, `Contents/Resources/`
  - Copy `Info.plist` with version substitution via `sed`
  - Copy `sigil.icns` to Resources
  - Files: `packaging/macos/create-app-bundle.sh`
  - Test: Script produces `Sigil.app/` with correct structure. `lipo -info Sigil.app/Contents/MacOS/sigild` shows `x86_64 arm64`
  - Depends: Task 2.1, Task 2.2, Task 2.3

- [ ] **Task 2.5**: Wire `make package-macos` to call `create-app-bundle.sh`
  - Replace stub target with actual invocation
  - Build Go binaries for both `darwin/amd64` and `darwin/arm64` before calling script
  - Files: `Makefile`
  - Test: `make package-macos` on macOS produces `Sigil.app/` with universal binaries
  - Depends: Task 1.2, Task 2.4

**Phase 2 verification:** `make package-macos` produces a valid `.app` bundle. Universal binaries contain both architectures. `Info.plist` has correct metadata. Icon renders correctly.

---

### Phase 3: macOS .dmg Creation

- [ ] **Task 3.1**: Create `.dmg` background image
  - Design a simple background (660x400) with arrow indicating drag-to-Applications
  - Include Sigil branding (logo, name, tagline)
  - Files: `packaging/macos/dmg-background.png`
  - Test: Image is correct dimensions and renders clearly
  - Depends: none

- [ ] **Task 3.2**: Implement `create-dmg.sh`
  - Accept `--version` and `--app-path` flags
  - Use `create-dmg` (npm package) for polished layout with icon positioning
  - Fall back to `hdiutil` if `create-dmg` is not available
  - Set volume name, icon, background, window size, icon positions
  - Output: `Sigil-VERSION-universal.dmg`
  - Files: `packaging/macos/create-dmg.sh`
  - Test: Script produces `.dmg`. Opening shows app icon + Applications alias on background
  - Depends: Task 3.1

- [ ] **Task 3.3**: Wire `make package-macos` to produce `.dmg`
  - Chain: build binaries -> create-app-bundle.sh -> create-dmg.sh
  - Final output in `dist/` directory
  - Files: `Makefile`
  - Test: `make package-macos` produces `dist/Sigil-VERSION-universal.dmg`
  - Depends: Task 2.5, Task 3.2

**Phase 3 verification:** `make package-macos` produces a `.dmg`. Opening the `.dmg` shows drag-to-Applications layout. Dragging installs `Sigil.app` to Applications.

---

### Phase 4: macOS Code Signing + Notarization

- [ ] **Task 4.1**: Implement `sign-and-notarize.sh`
  - Accept `--app-path` and `--dmg-path` flags
  - Detect `APPLE_DEVELOPER_ID`, `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` environment variables
  - If credentials missing: print warning and exit 0 (unsigned build is valid)
  - If credentials present: `codesign --deep` the `.app`, `codesign` the `.dmg`, `notarytool submit`, `stapler staple`
  - Files: `packaging/scripts/sign-and-notarize.sh`
  - Test: Without credentials, script exits 0 with warning. With test credentials (if available), signing succeeds.
  - Depends: none

- [ ] **Task 4.2**: Integrate signing into `create-app-bundle.sh` and `create-dmg.sh`
  - Call `sign-and-notarize.sh` at the end of each script
  - Signing is optional — scripts complete successfully without it
  - Files: `packaging/macos/create-app-bundle.sh`, `packaging/macos/create-dmg.sh`
  - Test: Full pipeline works with and without signing credentials
  - Depends: Task 4.1, Task 2.4, Task 3.2

**Phase 4 verification:** Build completes without credentials (unsigned). When credentials are provided, `codesign --verify` passes on the `.app` bundle.

---

### Phase 5: Windows .msi Installer

- [ ] **Task 5.1**: Create `sigil.ico` Windows icon file [P]
  - Convert `assets/icon.png` to `.ico` format with sizes: 16, 32, 48, 64, 128, 256
  - Use ImageMagick `convert` or similar tool
  - Files: `packaging/windows/sigil.ico`
  - Test: `file packaging/windows/sigil.ico` reports valid ico
  - Depends: none

- [ ] **Task 5.2**: Create WiX installer banner and dialog images [P]
  - `banner.bmp` — 493x58, shown at top of installer wizard
  - `dialog.bmp` — 493x312, shown on first/last page of installer
  - Include Sigil branding
  - Files: `packaging/windows/banner.bmp`, `packaging/windows/dialog.bmp`
  - Test: Images are correct dimensions and format
  - Depends: none

- [ ] **Task 5.3**: Write WiX v4 installer definition
  - Define directory structure: `ProgramFiles\Sigil\`
  - Component for binaries: `sigild.exe`, `sigilctl.exe`, `sigil-app.exe`
  - Component for PATH modification via `Environment` element
  - Component for service registration via `ServiceInstall` and `ServiceControl`
  - Component for auto-start via `RegistryValue` in `HKCU\...\Run`
  - Component for Start Menu shortcuts
  - Support for upgrade (MajorUpgrade element) and uninstall
  - Files: `packaging/windows/sigil.wxs`
  - Test: `wix build` succeeds on Windows runner with test binaries
  - Depends: Task 5.1, Task 5.2

- [ ] **Task 5.4**: Implement `build-msi.ps1` build script
  - Accept `-Version` parameter
  - Install WiX v4 via `dotnet tool install` if not present
  - Call `wix build` with version variable substitution
  - Output: `Sigil-VERSION-x64.msi`
  - Files: `packaging/windows/build-msi.ps1`
  - Test: Script produces `.msi` on Windows. Silent install (`msiexec /quiet`) succeeds.
  - Depends: Task 5.3

- [ ] **Task 5.5**: Test MSI installation lifecycle
  - Install: binaries in Program Files, PATH updated, service registered, Start Menu shortcut created
  - Upgrade: new version installs over old without data loss
  - Uninstall: all files removed, PATH entry removed, service unregistered, registry cleaned
  - Files: none (testing only)
  - Test: Full install -> verify -> upgrade -> verify -> uninstall -> verify cycle on Windows
  - Depends: Task 5.4

- [ ] **Task 5.6**: Wire `make package-windows` to call `build-msi.ps1`
  - Replace stub target with actual invocation
  - Requires Windows runner or cross-build setup
  - Files: `Makefile`
  - Test: `make package-windows` on Windows produces `dist/Sigil-VERSION-x64.msi`
  - Depends: Task 1.2, Task 5.4

**Phase 5 verification:** `.msi` installs cleanly on Windows 10/11. `sigild --version` works from any terminal after install. Service auto-starts. Start Menu shortcut launches `sigil-app`. Clean uninstall removes everything.

---

### Phase 6: Linux AppImage

- [ ] **Task 6.1**: Create XDG `.desktop` file for `sigil-app`
  - Standard desktop entry: `Name=Sigil`, `Exec=sigil-app`, `Icon=sigil`, `Type=Application`
  - Categories: `Utility;Development;`
  - Files: `packaging/linux/sigil.desktop`
  - Test: `desktop-file-validate packaging/linux/sigil.desktop` passes
  - Depends: none

- [ ] **Task 6.2**: Create AppStream metadata [P]
  - Standard AppStream `appdata.xml` for software center integration
  - Include description, screenshots placeholder, release history
  - Files: `packaging/linux/appimage/sigil.appdata.xml`
  - Test: `appstreamcli validate packaging/linux/appimage/sigil.appdata.xml` passes (if available)
  - Depends: none

- [ ] **Task 6.3**: Create `AppRun` entry point script
  - Detect invocation name to dispatch to correct binary (`sigild`, `sigilctl`, or default `sigil-app`)
  - Set up `LD_LIBRARY_PATH` if needed
  - Files: `packaging/linux/appimage/AppRun`
  - Test: Script dispatches correctly based on `$0`
  - Depends: none

- [ ] **Task 6.4**: Implement `build-appimage.sh`
  - Accept `--version`, `--arch`, `--bin-dir` flags
  - Assemble `AppDir` structure with binaries, `.desktop`, icon, `AppRun`
  - Download `appimagetool` if not present (from GitHub releases)
  - Build AppImage: `ARCH=$ARCH appimagetool Sigil.AppDir output.AppImage`
  - Files: `packaging/linux/build-appimage.sh`
  - Test: Script produces runnable AppImage on Ubuntu 20.04+
  - Depends: Task 6.1, Task 6.2, Task 6.3

- [ ] **Task 6.5**: Wire `make package-linux` to build AppImage
  - Replace stub target with actual invocation (AppImage portion)
  - Files: `Makefile`
  - Test: `make package-linux` on Linux produces `dist/Sigil-VERSION-x86_64.AppImage`
  - Depends: Task 1.2, Task 6.4

**Phase 6 verification:** AppImage runs on Ubuntu 20.04+ without installation. `./Sigil.AppImage` launches `sigil-app`. Symlink trick (`ln -s Sigil.AppImage sigild && ./sigild --version`) works.

---

### Phase 7: Linux .deb and .rpm Packages

- [ ] **Task 7.1**: Create Debian control file template
  - Package metadata: name, version (placeholder), architecture, maintainer, description
  - Dependencies: `libc6 (>= 2.17)` (minimal, no heavy deps)
  - Files: `packaging/linux/deb/control`
  - Test: Template renders valid control file with version substitution
  - Depends: none

- [ ] **Task 7.2**: Create Debian post-install and pre-removal scripts
  - `postinst`: reload systemd user daemon, print enable instructions
  - `prerm`: disable and stop sigild user service
  - `conffiles`: list `/etc/sigil/config.example.toml`
  - Files: `packaging/linux/deb/postinst`, `packaging/linux/deb/prerm`, `packaging/linux/deb/conffiles`
  - Test: Scripts are syntactically valid shell
  - Depends: none

- [ ] **Task 7.3**: Implement `build-deb.sh`
  - Accept `--version`, `--arch`, `--bin-dir` flags
  - Stage directory structure: `usr/bin/`, `usr/lib/systemd/user/`, `usr/share/applications/`, `usr/share/icons/`, `etc/sigil/`
  - Copy binaries, service file, desktop file, icon, config example
  - Generate `DEBIAN/control` from template with version substitution
  - Copy maintainer scripts to `DEBIAN/`
  - Build with `dpkg-deb --build`
  - Files: `packaging/linux/build-deb.sh`
  - Test: `dpkg-deb --build` produces installable `.deb`. `dpkg -i` succeeds on Ubuntu.
  - Depends: Task 6.1, Task 7.1, Task 7.2

- [ ] **Task 7.4**: Create RPM spec file
  - Package metadata matching Debian control
  - `%install` section placing files in same locations as `.deb`
  - `%post` and `%preun` scriptlets matching Debian maintainer scripts
  - `%config(noreplace)` for config file
  - Files: `packaging/linux/rpm/sigil.spec`
  - Test: Spec file syntax is valid
  - Depends: none

- [ ] **Task 7.5**: Implement `build-rpm.sh`
  - Accept `--version`, `--arch`, `--bin-dir` flags
  - Set up rpmbuild directory structure
  - Copy sources into `SOURCES/`
  - Run `rpmbuild -bb` with version substitution
  - Files: `packaging/linux/build-rpm.sh`
  - Test: `rpmbuild` produces installable `.rpm`. `rpm -i` succeeds on Fedora (container).
  - Depends: Task 6.1, Task 7.4

- [ ] **Task 7.6**: Copy and adapt systemd service file
  - Copy `deploy/sigild.service` to `packaging/linux/sigild.service`
  - Ensure it is a user service (`[Install] WantedBy=default.target`)
  - Verify `ExecStart=/usr/bin/sigild` path matches package layout
  - Files: `packaging/linux/sigild.service`
  - Test: `systemd-analyze verify packaging/linux/sigild.service` passes
  - Depends: none

- [ ] **Task 7.7**: Wire `make package-linux` to build .deb and .rpm
  - Extend the Linux packaging target to build all three formats (AppImage + deb + rpm)
  - Files: `Makefile`
  - Test: `make package-linux` produces AppImage, `.deb`, and `.rpm` in `dist/`
  - Depends: Task 6.5, Task 7.3, Task 7.5

**Phase 7 verification:** `dpkg -i` installs `.deb` on Ubuntu. `rpm -i` installs `.rpm` on Fedora. Both place binaries in `/usr/bin/`, service in `/usr/lib/systemd/user/`, desktop file in `/usr/share/applications/`. `dpkg -r sigil` and `rpm -e sigil` cleanly uninstall.

---

### Phase 8: CI/CD Release Workflow

- [ ] **Task 8.1**: Add `windows/amd64` to existing release build matrix
  - The current release workflow builds `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
  - Add `windows/amd64` entry with `GOOS: windows`
  - Produce `sigild-windows-amd64.exe`, `sigilctl-windows-amd64.exe`, `sigil-app-windows-amd64.exe`
  - Files: `.github/workflows/release.yml`
  - Test: CI produces Windows binaries alongside existing platform binaries
  - Depends: none

- [ ] **Task 8.2**: Add `package-macos` CI job
  - `runs-on: macos-latest`, `needs: build`
  - Download `darwin/amd64` and `darwin/arm64` artifacts
  - Run `packaging/macos/create-app-bundle.sh` and `packaging/macos/create-dmg.sh`
  - Optionally sign if `APPLE_DEVELOPER_ID` secret is set
  - Upload `.dmg` as artifact
  - Files: `.github/workflows/release.yml`
  - Test: CI job produces `.dmg` artifact
  - Depends: Task 3.3

- [ ] **Task 8.3**: Add `package-windows` CI job
  - `runs-on: windows-latest`, `needs: build`
  - Download `windows/amd64` artifacts
  - Install WiX v4 via `dotnet tool install`
  - Run `packaging/windows/build-msi.ps1`
  - Upload `.msi` as artifact
  - Files: `.github/workflows/release.yml`
  - Test: CI job produces `.msi` artifact
  - Depends: Task 5.6, Task 8.1

- [ ] **Task 8.4**: Add `package-linux` CI job
  - `runs-on: ubuntu-latest`, `needs: build`
  - Download `linux/amd64` and `linux/arm64` artifacts
  - Install build tools: `appimagetool`, `rpmbuild` (via `rpm` package)
  - Run all three build scripts for each architecture
  - Upload AppImage, `.deb`, `.rpm` as artifacts
  - Files: `.github/workflows/release.yml`
  - Test: CI job produces AppImage, `.deb`, `.rpm` artifacts for both architectures
  - Depends: Task 7.7

- [ ] **Task 8.5**: Extend release job to upload all installer artifacts
  - Download artifacts from all packaging jobs
  - Include installer artifacts in `softprops/action-gh-release` files list
  - Generate checksums for all new artifacts alongside existing checksums
  - Files: `.github/workflows/release.yml`
  - Test: Tagged release includes bare binaries + `.dmg` + `.msi` + AppImage + `.deb` + `.rpm` + checksums
  - Depends: Task 8.2, Task 8.3, Task 8.4

- [ ] **Task 8.6**: Add CI caching for packaging dependencies
  - Cache Go modules (already cached)
  - Cache npm packages for `create-dmg` on macOS
  - Cache .NET tools for WiX on Windows
  - Cache `appimagetool` download on Linux
  - Files: `.github/workflows/release.yml`
  - Test: Second CI run is faster due to cache hits
  - Depends: Task 8.5

**Phase 8 verification:** Push a test tag. CI produces all artifacts. GitHub Release page shows all installer formats with checksums.

---

### Phase 9: Package Manager Manifests

- [ ] **Task 9.1**: Create Homebrew cask formula
  - Cask that downloads `.dmg` from GitHub Releases
  - `app "Sigil.app"` stanza, symlinks for `sigild` and `sigilctl` to `/usr/local/bin/`
  - `uninstall quit: "tech.sigil.app"`, `zap` stanza for config cleanup
  - Files: `packaging/macos/homebrew-cask.rb`
  - Test: Formula passes `brew audit --cask` (local validation)
  - Depends: Task 3.3

- [ ] **Task 9.2**: Create Homebrew cask auto-update workflow
  - Triggered on `release` event (published)
  - Updates cask formula with new version and SHA256
  - Submits PR to the Homebrew tap repository
  - Files: `.github/workflows/homebrew-cask.yml`
  - Test: Workflow triggers on release and produces valid PR
  - Depends: Task 9.1

- [ ] **Task 9.3**: Create winget manifest files
  - Three files: version manifest, installer manifest, locale manifest
  - Follow `microsoft/winget-pkgs` format (ManifestVersion 1.6.0)
  - Installer type: `msi`, architecture: `x64`
  - Files: `packaging/windows/winget/Sigil.Sigil.yaml`, `packaging/windows/winget/Sigil.Sigil.installer.yaml`, `packaging/windows/winget/Sigil.Sigil.locale.en-US.yaml`
  - Test: `winget validate` passes on all three files
  - Depends: Task 5.4

- [ ] **Task 9.4**: Create Scoop manifest
  - JSON manifest pointing to `.msi` release artifact
  - Include SHA256 hash, installer/uninstaller scripts
  - Files: `packaging/windows/scoop/sigil.json`
  - Test: Scoop manifest validates against Scoop schema
  - Depends: Task 5.4

- [ ] **Task 9.5**: Create winget/scoop auto-update workflow [P]
  - Triggered on `release` event
  - Updates winget manifest with new version and hash, submits PR to `microsoft/winget-pkgs`
  - Updates scoop manifest in the Sigil bucket repository
  - Files: `.github/workflows/package-managers.yml`
  - Test: Workflow triggers and produces valid manifest updates
  - Depends: Task 9.3, Task 9.4

**Phase 9 verification:** `brew install --cask sigil` installs from `.dmg` (once tap is set up). winget and scoop manifests pass validation. Auto-update workflows trigger on new releases.

---

### Phase 10: Update install.sh + Documentation

- [ ] **Task 10.1**: Update `install.sh` to install `sigil-app`
  - Add `sigil-app` to the download list alongside `sigild` and `sigilctl`
  - Handle the case where `sigil-app` binary is not in the release (older releases)
  - Update checksum verification to include `sigil-app`
  - Files: `scripts/install.sh`
  - Test: `install.sh` downloads and installs all three binaries. Works with releases that have `sigil-app` and gracefully skips when it is absent.
  - Depends: none

- [ ] **Task 10.2**: Update `install.sh` to create `.desktop` file on Linux
  - After installing `sigil-app`, create `~/.local/share/applications/sigil.desktop`
  - Only if `sigil-app` was installed and desktop environment is detected
  - Files: `scripts/install.sh`
  - Test: `.desktop` file created. `sigil-app` appears in application menu.
  - Depends: Task 10.1

- [ ] **Task 10.3**: Update CLAUDE.md with packaging commands
  - Add `make package-macos`, `make package-windows`, `make package-linux` to build commands
  - Document `packaging/` directory in architecture section
  - Files: `CLAUDE.md`
  - Test: Documentation is accurate
  - Depends: Task 1.2

- [ ] **Task 10.4**: Update CONTRIBUTING.md with installer build instructions
  - Add section on building installers locally
  - Document required tools per platform (`create-dmg`, WiX, `appimagetool`, `rpmbuild`)
  - Document CI workflow for contributors
  - Files: `CONTRIBUTING.md`
  - Test: A new contributor can follow instructions to build an installer
  - Depends: Task 8.5

- [ ] **Task 10.5**: Update `.github/pull_request_template.md`
  - Add checklist item for installer CI if packaging files are modified
  - Files: `.github/pull_request_template.md`
  - Test: Template includes installer check
  - Depends: Task 8.5

- [ ] **Task 10.6**: Cross-platform installation smoke test
  - Manual end-to-end test on macOS (arm64), Windows 10, Ubuntu 22.04, Fedora 39
  - Verify: install, PATH works, service starts, tray app launches, uninstall clean
  - Test all three methods per platform: GUI installer, package manager, `install.sh` (Linux)
  - Files: none (testing only)
  - Test: All success criteria from spec pass on each platform
  - Depends: all previous tasks

**Phase 10 verification:** `install.sh` installs three binaries. Documentation is accurate. Cross-platform smoke test passes. All spec success criteria met.

---

## Summary

| Phase | Tasks | Parallelizable | Estimated Effort |
|-------|-------|----------------|-----------------|
| 1. Scaffold | 3 | 1 | Small |
| 2. macOS .app Bundle | 5 | 2 | Medium |
| 3. macOS .dmg | 3 | 1 | Small |
| 4. macOS Signing | 2 | 0 | Small |
| 5. Windows .msi | 6 | 2 | Medium |
| 6. Linux AppImage | 5 | 2 | Medium |
| 7. Linux .deb + .rpm | 7 | 3 | Medium |
| 8. CI/CD Release | 6 | 0 | Medium |
| 9. Package Managers | 5 | 2 | Medium |
| 10. install.sh + Docs | 6 | 3 | Small |
| **Total** | **48** | **16** | |
