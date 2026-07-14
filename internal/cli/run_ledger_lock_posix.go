//go:build !windows

package cli

import (
	"fmt"
	"os"
	"syscall"
)

func openRunLedgerLockFile(path string) (*os.File, error) {
	descriptor, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = syscall.Close(descriptor)
		return nil, fmt.Errorf("open embedded run ledger lock %s", path)
	}
	return file, nil
}

func lockRunLedgerFile(file *os.File, exclusive bool) (func() error, error) {
	operation := syscall.LOCK_SH
	if exclusive {
		operation = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		return nil, err
	}
	return func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}, nil
}
