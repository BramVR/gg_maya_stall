//go:build !windows

package cli

import (
	"errors"
	"os"
)

func syncRunLedgerDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
