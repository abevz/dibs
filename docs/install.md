# Installing dibs

The `v0.1.0-rc.1` preview is published for Linux amd64/arm64 and macOS
Intel/Apple Silicon. Use its versioned URL: GitHub's `latest` URL selects
stable releases and does not select this prerelease.

## Linux and macOS release installation

Install the published preview:

```sh
curl -fsSL https://github.com/abevz/dibs/releases/download/v0.1.0-rc.1/install.sh | sh /dev/stdin
```

The downloaded script carries the release tag that supplied it, and fetches the
archive and checksum manifest for that same tag. It selects Linux amd64/arm64
or macOS Intel/Apple Silicon, verifies the archive, and installs `dibs`,
`dibsd`, and `dibs-mcp` in `~/.local/bin` without sudo. It installs the Apache-2.0
license at `~/.local/share/licenses/dibs/LICENSE`. The script prints a PATH
hint when that directory is not available in the current shell.

To inspect the exact script before running it:

```sh
curl -fsSL -o install.sh https://github.com/abevz/dibs/releases/download/v0.1.0-rc.1/install.sh
less install.sh
sh install.sh
```

To install a specific published version, download its script from that tag:

```sh
curl -fsSL https://github.com/abevz/dibs/releases/download/vX.Y.Z/install.sh | sh /dev/stdin
```

Replace `vX.Y.Z` with a release tag. Repeating the installer replaces the three
program binaries and preserves the daemon database and local state. Installing
a different tag updates or downgrades only those binaries; check compatibility
with the existing database before a downgrade. To inspect the running client
version, use `dibs version`, and use `dibs doctor` after the daemon is running.

The installer does not switch an already running service to a new binary. A
service switch or restart is an explicit operator action described in
[operations](operations.md#explicit-service-switch). Existing installations
using the former `af-coordinator` paths keep their existing canonical database.

## First use

Run `dibs init` inside a Git repository. It displays the detected project,
repository, worktree, and branch, starts `dibsd` when needed, registers the
mapping, and adds the managed agent instructions to the repository's
`AGENTS.md`. Repeating `dibs init` is safe. Example after installation:

```sh
cd your-repository
dibs init
dibs issue create --project your-repository --scope-kind project --title "First task"
dibs issue claim your-repository-1 --holder "$USER"
```

Use the project key shown by `init` in the latter commands. If Git cannot
unambiguously identify the project or default branch, `init` asks for
`--project`, `--repo`, or `--default-branch`. For an inspectable preview without
writing files or starting the daemon, run `dibs init --dry-run`.

`dibs daemon start` and `dibs daemon stop` control a daemon started by dibs.
For a systemd or launchd managed daemon, stop it with its service manager;
`daemon stop` refuses to signal a manager-owned process.
The daemon uses the existing database path, including the legacy path when
present. Startup errors are shown with the daemon log path. Service-manager
setups remain available for users who want them.

## Remove binaries

Stop a running daemon first, using the applicable commands in
[operations](operations.md). For the default release installation directory:

```sh
rm "$HOME/.local/bin/dibs" "$HOME/.local/bin/dibsd" "$HOME/.local/bin/dibs-mcp"
rm "$HOME/.local/share/licenses/dibs/LICENSE"
```

The release installer also creates legacy command aliases `afctl`,
`af-coordinatord`, and `afc-mcp` in the same directory. Remove those aliases
only when they still point to the dibs binaries. The database, backups,
configuration, and logs stay on disk so removal of the programs does not
discard work.

## Other channels

The release workflow prepares a versioned Homebrew formula using the same
archive checksums. A tap, `go install`, and AUR instructions will be advertised
after each channel is installed and checked on its target environment.
