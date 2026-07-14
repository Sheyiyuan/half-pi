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
	return filepath.Join(appData, "half-pi"), nil
}
