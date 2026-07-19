//go:build windows

package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

func halfPiHome() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA environment variable is not set")
	}
	home, err := filepath.Abs(filepath.Join(appData, "half-pi"))
	if err != nil {
		return "", fmt.Errorf("resolve half-pi home: %w", err)
	}
	return filepath.Clean(home), nil
}
