//go:build !windows

package cli

import "os"

func openRunLedgerRead(path string) (*os.File, error) {
	return os.Open(path)
}
