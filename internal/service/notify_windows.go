//go:build windows

package service

import (
	"os/exec"
	"strings"
)

func reportShellError(err error) {
	showMessageBox(err.Error(), "zip233 操作失败", "Error")
}

func reportShellSuccess(message string) {
	showMessageBox(message, "zip233", "Information")
}

func showMessageBox(message, title, icon string) {
	ps := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	script := "Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.MessageBox]::Show(" +
		ps(message) + ", " + ps(title) + ", 'OK', '" + icon + "')"
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	_ = cmd.Run()
}
