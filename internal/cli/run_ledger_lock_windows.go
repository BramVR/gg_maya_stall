//go:build windows

package cli

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const lockFileExclusiveLock = 0x00000002

var (
	kernel32LockFileEx   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	kernel32UnlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

func openRunLedgerLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|int(syscall.FILE_FLAG_OPEN_REPARSE_POINT), 0o600)
}

func lockRunLedgerFile(file *os.File, exclusive bool) (func() error, error) {
	overlapped := new(syscall.Overlapped)
	flags := uintptr(0)
	if exclusive {
		flags = lockFileExclusiveLock
	}
	result, _, callErr := kernel32LockFileEx.Call(file.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	if result == 0 {
		return nil, fmt.Errorf("lock embedded run ledger: %w", callErr)
	}
	return func() error {
		result, _, callErr := kernel32UnlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
		if result == 0 {
			return fmt.Errorf("unlock embedded run ledger: %w", callErr)
		}
		return nil
	}, nil
}
