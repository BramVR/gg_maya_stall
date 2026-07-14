package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func withRunLedgerLock(repoDir string, runID string, action func() error) (err error) {
	if err := validateRunID(runID); err != nil {
		return err
	}
	return withRunLedgerRootLock(repoDir, false, func() error {
		info, err := os.Lstat(runLedgerDir(repoDir, runID))
		if errors.Is(err, os.ErrNotExist) {
			return newUsageError("run %q not found", runID)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("embedded run ledger path %s must be a directory, not a symlink", runLedgerDir(repoDir, runID))
		}
		lockPath := filepath.Join(runLedgerDir(repoDir, runID), ".update.lock")
		if err := ensureWorkspacePathHasNoSymlinkAncestor(runLedgerDir(repoDir, runID), ".update.lock"); err != nil {
			return err
		}
		return withRunLedgerFileLock(lockPath, true, action)
	})
}

func withRunLedgerRootLock(repoDir string, exclusive bool, action func() error) error {
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs")); err != nil {
		return err
	}
	if err := os.MkdirAll(runLedgerRoot(repoDir), 0o755); err != nil {
		return err
	}
	return withRunLedgerFileLock(filepath.Join(runLedgerRoot(repoDir), ".ledger.lock"), exclusive, action)
}

func withRunLedgerFileLock(lockPath string, exclusive bool, action func() error) (err error) {
	file, err := openRunLedgerLockFile(lockPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return fmt.Errorf("embedded run ledger update lock %s must be a regular file, not a symlink", lockPath)
	}
	unlock, err := lockRunLedgerFile(file, exclusive)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return action()
}
