//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// windowsNotifier uses PowerShell BalloonTip notifications.
// For full toast notification support with action buttons, a future version
// should use go-toast/toast.
type windowsNotifier struct{}

func newNotifier() Notifier {
	return &windowsNotifier{}
}

func (n *windowsNotifier) Show(title, body, iconPath string, suggestionID int64) error {
	// PowerShell BalloonTip notification.
	safeTitle := strings.ReplaceAll(title, "'", "''")
	safeBody := strings.ReplaceAll(body, "'", "''")

	script := fmt.Sprintf(`
[void] [System.Reflection.Assembly]::LoadWithPartialName("System.Windows.Forms")
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipTitle = '%s'
$n.BalloonTipText = '%s'
$n.Visible = $True
$n.ShowBalloonTip(5000)
Start-Sleep -Seconds 6
$n.Dispose()
`, safeTitle, safeBody)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	return cmd.Run()
}

func (n *windowsNotifier) Close() {}
