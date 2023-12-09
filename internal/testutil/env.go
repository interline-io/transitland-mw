package testutil

import (
	"fmt"
	"os"
)

// Test helpers

func CheckEnv(key string) (string, string, bool) {
	g := os.Getenv(key)
	if g == "" {
		return "", fmt.Sprintf("%s is not set, skipping", key), false
	}
	return g, "", true
}
