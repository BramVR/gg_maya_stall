//go:build windows

package cli

import (
	"errors"
	"syscall"
)

const processQueryLimitedInformation = 0x1000

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return true
	}
	if err != nil {
		return false
	}
	if handle != 0 {
		_ = syscall.CloseHandle(handle)
	}
	return true
}
