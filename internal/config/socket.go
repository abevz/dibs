package config

import (
	"fmt"
	"syscall"
)

// ValidateSocketPath checks the byte length of a pathname Unix socket. The
// sockaddr path also needs room for its terminating NUL byte.
func ValidateSocketPath(path string) error {
	capacity := len(syscall.RawSockaddrUnix{}.Path)
	if len(path) < capacity {
		return nil
	}
	return fmt.Errorf("unix socket path %q is %d bytes; sun_path limit is %d bytes (maximum pathname %d bytes); set DIBS_SOCKET to a shorter path, for example under $XDG_RUNTIME_DIR", path, len(path), capacity, capacity-1)
}
