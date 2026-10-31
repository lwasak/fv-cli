# Install

`fv` is one static binary with no runtime dependencies. It is built with `CGO_ENABLED=0` for these targets: `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`.

> **Binaries are not code-signed.** Release archives are built by GitHub Actions and come with SHA-256 checksums, but there is no Windows code signature and no Apple notarization. The sections below say what that means on each OS.

## Download a release

Every tagged version is published on the [GitHub Releases page](https://github.com/lwasak/fv-cli/releases) with one archive per platform (`.tar.gz`, and `.zip` for Windows) and a `checksums.txt` file. Each archive holds the binary, the README and this user guide.

Verify the download before you run it:

```powershell
# Linux (macOS: shasum -a 256 --check --ignore-missing checksums.txt)
sha256sum --check --ignore-missing checksums.txt
# Windows: compare the hash with the matching line of checksums.txt
Get-FileHash .\fv_*_windows_amd64.zip -Algorithm SHA256
```

Then unpack the archive and follow the section for your OS below.

## Install with go install

With Go 1.26.10 or newer:

```powershell
go install github.com/lwasak/fv-cli/cmd/fv@latest
```

This puts `fv` (`fv.exe` on Windows) in `$(go env GOPATH)/bin`; add that folder to your `PATH`. A binary built on your own machine is never quarantined or blocked (see the OS sections). `fv version` prints `dev` for it because no version is stamped in.

## Build from source

You need Go 1.26.10 or newer (the `go` line of `go.mod`). From the `fv` folder:

```powershell
go build ./cmd/fv
```

This writes `fv.exe` (Windows) or `fv` (macOS, Linux) in the current folder. To stamp a version, commit and date into `fv version`:

```powershell
$commit = git rev-parse --short HEAD
$date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd")
go build -trimpath -ldflags "-X github.com/lwasak/fv-cli/internal/cli.Version=1.0.0 -X github.com/lwasak/fv-cli/internal/cli.Commit=$commit -X github.com/lwasak/fv-cli/internal/cli.Date=$date" -o fv.exe ./cmd/fv
```

Check the result:

```powershell
.\fv.exe version
```

`fv version` prints one line: the version, commit, build date and platform. A build without the flags prints `dev` and `unknown`.

### Cross-compile for every target

Go cross-compiles without a C toolchain because `fv` does not use cgo. This PowerShell 7 script builds all five targets into `dist/`:

```powershell
$env:CGO_ENABLED = "0"
$targets = "windows/amd64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"
New-Item -ItemType Directory -Force dist | Out-Null
foreach ($t in $targets) {
    $os, $arch = $t -split "/"
    $env:GOOS = $os
    $env:GOARCH = $arch
    $ext = if ($os -eq "windows") { ".exe" } else { "" }
    go build -trimpath -o "dist/fv-$os-$arch$ext" ./cmd/fv
    if ($LASTEXITCODE -ne 0) { throw "build failed for $t" }
}
Remove-Item Env:GOOS, Env:GOARCH
Get-ChildItem dist
```

On macOS and Linux with PowerShell 7 (`pwsh`) the same script works unchanged. For a single target, set the two variables yourself, for example `$env:GOOS = "linux"; $env:GOARCH = "arm64"`.

A binary built for another OS cannot run on the machine that built it. Copy it over and, on Linux and macOS, make it executable (see below).

## Windows

1. Unpack the `windows_amd64` archive from the Releases page, or build `fv.exe` (above).
2. Put it in a folder on your `PATH`, for example `C:\Tools\fv\fv.exe`. In PowerShell:

```powershell
New-Item -ItemType Directory -Force C:\Tools\fv | Out-Null
Copy-Item .\fv.exe C:\Tools\fv\fv.exe
[Environment]::SetEnvironmentVariable("Path", $env:Path + ";C:\Tools\fv", "User")
```

3. Open a new terminal and run `fv version`.

**SmartScreen.** SmartScreen only checks files that carry the "Mark of the Web", which browsers add to downloads. Running `fv` from a terminal does not trigger SmartScreen. If you downloaded the archive in a browser and Windows blocks the unpacked file (the binary is not code-signed), unblock it once with `Unblock-File C:\Tools\fv\fv.exe` after you have checked the checksum. A binary you built yourself is not blocked.

Secrets go to Windows Credential Manager (see [Setup](setup.md)). Use PowerShell 7.5 or newer for the scripts in this guide, and read [Amounts](amounts.md) before you type an amount: Windows PowerShell 5.1 silently changes an unquoted `18500,05`.

## macOS

1. Take the `darwin_arm64` archive (Apple silicon) or the `darwin_amd64` archive (Intel) from the Releases page, or build for your Mac.
2. Make it executable and put it on your `PATH`:

```powershell
chmod +x ./fv
sudo mv ./fv /usr/local/bin/fv
```

**Gatekeeper.** Gatekeeper only affects quarantined files. A binary you built on the same Mac, or one fetched with `curl` (for example `curl -L -O <asset URL from the Releases page>`), runs without any warning. A file downloaded in a browser (or AirDropped, or a mail attachment) carries a quarantine attribute, and Gatekeeper refuses to run an unsigned, un-notarized binary: "fv cannot be opened because the developer cannot be verified", or the process is killed right away. fv has no Apple developer signature. After you have checked the checksum, remove the attribute once:

```powershell
xattr -d com.apple.quarantine /usr/local/bin/fv
```

If macOS still kills the binary (it happens with some copies of `darwin/arm64` builds), re-sign it ad hoc: `codesign --force --sign - /usr/local/bin/fv`. The other way is to allow it under System Settings, Privacy & Security, "Open Anyway", after the first refusal.

Secrets go to the macOS Keychain.

## Linux

1. Take the `linux_amd64` or `linux_arm64` archive from the Releases page, or build for your architecture.
2. Install it:

```powershell
chmod +x ./fv
sudo install -m 0755 ./fv /usr/local/bin/fv
```

3. Run `fv version`.

Secrets go to the desktop Secret Service (GNOME Keyring, KWallet) over D-Bus. On a server or in a container there is no Secret Service. fv then falls back to the `FV_SECRET_*` environment variable for the key passphrase and logs a warning; see [Setup](setup.md) and [Security](security.md) for what that costs you.

## Shell completion

```powershell
fv completion powershell | Out-String | Invoke-Expression
```

Add that line to your `$PROFILE` to keep it. For bash, zsh and fish run `fv completion bash --help` (or `zsh`, `fish`) for the install lines. Completion knows commands, flags and the three `--env` values.

## Next

Continue with [Setup](setup.md).
