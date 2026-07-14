//go:build !windows

package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

func halfPiHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".half-pi"), nil
}
