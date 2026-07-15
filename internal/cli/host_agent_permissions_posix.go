//go:build !windows

package cli

import (
	"fmt"
	"io/fs"
)

func validateHostAgentDirectoryPermissions(path string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("Windows Host Agent directory %s must be private (0700 or stricter)", path) //nolint:staticcheck // Product term starts the user-facing diagnostic.
	}
	return nil
}
