//go:build !windows

package shell

import (
	"errors"

	"github.com/neko233-com/zip233/internal/contract"
)

// Status always reports disabled on non-Windows platforms.
func Status() contract.ShellStatus { return contract.ShellStatus{} }

// Register is only available on Windows.
func Register() (contract.ShellStatus, error) {
	return Status(), errors.New("Windows 右键菜单集成仅在 Windows 上可用。")
}

// Unregister is only available on Windows.
func Unregister() (contract.ShellStatus, error) {
	return Status(), errors.New("Windows 右键菜单集成仅在 Windows 上可用。")
}

// RepairIfEnabled is a no-op on non-Windows platforms.
func RepairIfEnabled() {}
