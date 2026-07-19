//go:build !windows

package setup

import "path/filepath"

func controlEndpoint(env *Env) string {
	return filepath.Join(env.RunDir, "mind-admin.sock")
}
