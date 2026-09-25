# Installing dibs

The first GitHub release has not been published yet. The commands below become
usable after the release workflow publishes its verified `install.sh`, archives,
and `checksums.txt` assets. The README continues to show the working source
build until that publication is complete.

## Linux and macOS release installation

The supported one-line entrypoint will be:

```sh
curl -fsSL https://github.com/abevz/dibs/releases/latest/download/install.sh | sh /dev/stdin
```

The downloaded script carries the release tag that supplied it, and fetches the
archive and checksum manifest for that same tag. It selects Linux amd64/arm64
or macOS Intel/Apple Silicon, verifies the archive, and installs `dibs`,
`dibsd`, and `dibs-mcp` in `~/.local/bin` without sudo. It installs the Apache-2.0
license at `~/.local/share/licenses/dibs/LICENSE`. The script prints a PATH
hint when that directory is not available in the current shell.

To inspect the exact script before running it:

```sh
curl -fsSL -o install.sh https://github.com/abevz/dibs/releases/latest/download/install.sh
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
