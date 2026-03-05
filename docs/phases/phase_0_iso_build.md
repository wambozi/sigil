# Phase 0 — ISO Build: Completed

## Status: Done
Custom NixOS ISO built, committed, and flashed to USB. Ready to install when NVMe arrives.

---

## Environment

- **Build machine:** Windows 10 Pro (Ryzen 5 3600)
- **Target machine:** 2017 MacBook Pro (Intel, Broadcom BCM43xx Wi-Fi) — storage blown, NVMe on order
- **USB:** 128GB stick, flashed with balenaEtcher

---

## What Was Built

A custom NixOS installer ISO (`nixos-minimal-25.05.20260102.ac62194-x86_64-linux.iso`) with:
- Broadcom `wl` driver bundled (required for BCM43xx Wi-Fi during install)
- `broadcom_sta` kernel module loaded at boot
- Conflicting drivers blacklisted (`b43`, `bcma`, `brcmfmac`, `brcmsmac`)
- Basic installer tools: `vim`, `git`, `wget`, `parted`

Source files committed to this repo: `flake.nix`, `iso.nix`, `flake.lock`

---

## Build Process

### Prerequisites (Windows)
1. Enabled **SVM Mode** in BIOS (AMD virtualization — under Advanced CPU settings)
2. Installed WSL2 + Ubuntu via `wsl --install ubuntu`
3. Installed Nix inside WSL2:
   ```bash
   sh <(curl -L https://nixos.org/nix/install) --daemon
   ```
4. Enabled flakes:
   ```bash
   mkdir -p ~/.config/nix
   echo "experimental-features = nix-command flakes" >> ~/.config/nix/nix.conf
   ```

### Build
```bash
mkdir ~/aether-iso && cd ~/aether-iso
# create flake.nix and iso.nix (see source files in this repo)
nix build .#nixosConfigurations.iso.config.system.build.isoImage
```

Build time: ~30-40 min on first run (cold cache).

### Copy ISO to Windows
```bash
cp ~/aether-iso/result/iso/*.iso /mnt/c/Users/nick/workspace/sigil/
```

### Flash to USB
Used **balenaEtcher** on Windows. No pre-formatting needed — Etcher overwrites the drive.

---

## Issues Encountered

| Error | Fix |
|-------|-----|
| `broadcom-sta` marked insecure | Added `nixpkgs.config.permittedInsecurePackages = ["broadcom-sta-6.30.223.271-59-6.12.63"]` to `iso.nix` |
| `networking.networkmanager` conflicts with `networking.wireless` | Removed NetworkManager from `iso.nix` — minimal installer uses wpa_supplicant by default |
| Git push 403 | Switched remote from HTTPS to SSH (`git remote set-url origin git@github.com:wambozi/aether.git`) |

---

## Next Step

When the NVMe arrives:
1. Boot MacBook from USB
2. Partition the NVMe (`parted` is included in the ISO)
3. Run NixOS installer (see `phase_0.md` Part 2)
4. Pull down the system flake and apply config
