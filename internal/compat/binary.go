package compat

import (
	"fmt"
	"os"
	"path/filepath"
)

// WarnLegacyBinary keeps machine-readable stdout untouched for old entrypoints.
func WarnLegacyBinary(legacy, replacement string) {
	if filepath.Base(os.Args[0]) == legacy {
		fmt.Fprintf(os.Stderr, "%s is deprecated; use %s instead\n", legacy, replacement)
	}
}
