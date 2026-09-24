package main

import (
	"fmt"
	"os"
	"strings"
)

// leaseTokenSourceAvailable is used by the early argument validator. The
// actual value is read only by a lifecycle handler and never copied to argv.
func leaseTokenSourceAvailable() bool {
	canonical, set := os.LookupEnv("DIBS_LEASE_TOKEN")
	return canonical != "" || os.Getenv("DIBS_LEASE_TOKEN_FILE") != "" || (!set && os.Getenv("AF_LEASE_TOKEN") != "")
}

func leaseTokenFromEnvironment() (string, error) {
	canonical, set := os.LookupEnv("DIBS_LEASE_TOKEN")
	if canonical != "" {
		return canonical, nil
	}
	path := os.Getenv("DIBS_LEASE_TOKEN_FILE")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read DIBS_LEASE_TOKEN_FILE: %w", err)
		}
		token := strings.TrimRight(string(data), "\r\n")
		if token == "" {
			return "", fmt.Errorf("DIBS_LEASE_TOKEN_FILE is empty")
		}
		return token, nil
	}
	if !set {
		if token := os.Getenv("AF_LEASE_TOKEN"); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("--lease-token is required: use dibs issue run or DIBS_LEASE_TOKEN_FILE")
}
