package build

// Revision is the git commit SHA the running binary was built from. `make
// build`/`make build-install` set it automatically via ldflags to
// `git rev-parse HEAD`; it stays "unknown" for builds that skip the
// Makefile (plain `go build`, `go install`) or that ship a release tarball
// without embedding it. `dibs doctor` and the pre-command staleness check
// in `dibs` use it to detect a daemon still running an older commit than
// the source checkout it was rebuilt from.
var Revision = "unknown"
