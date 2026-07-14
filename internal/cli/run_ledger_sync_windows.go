//go:build windows

package cli

// Windows does not support flushing directory handles through os.File.Sync.
// Ledger files are still flushed before atomic replacement.
func syncRunLedgerDirectory(string) error {
	return nil
}
