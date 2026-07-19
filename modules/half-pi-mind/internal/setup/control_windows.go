//go:build windows

package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

func controlEndpoint(env *Env) string {
	canonicalHome := strings.ToLower(filepath.Clean(env.HomeDir))
	sum := sha256.Sum256([]byte(canonicalHome))
	return `\\.\pipe\half-pi-mind-admin-` + hex.EncodeToString(sum[:8])
}
