//go:build windows

package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHostAgentRejectsWindowsFilesystemRootsBeforeACLMutation(t *testing.T) {
	for _, path := range []string{`C:\`, `\\server\share\`} {
		if !isHostAgentFilesystemRoot(path) {
			t.Fatalf("Windows filesystem root %q was accepted", path)
		}
	}
	for _, path := range []string{`C:\maya-stall\agent`, `\\server\share\maya-stall\agent`} {
		if isHostAgentFilesystemRoot(path) {
			t.Fatalf("dedicated Windows Agent directory %q was rejected", path)
		}
	}
}

func TestHostAgentWindowsACLHardeningRemovesOtherAllowRules(t *testing.T) {
	path := t.TempDir()
	const grant = `$acl = Get-Acl -LiteralPath $args[0]
$everyone = New-Object System.Security.Principal.SecurityIdentifier -ArgumentList 'S-1-1-0'
$rule = New-Object System.Security.AccessControl.FileSystemAccessRule($everyone, 'Read', 'ContainerInherit, ObjectInherit', 'None', 'Allow')
$acl.AddAccessRule($rule)
Set-Acl -LiteralPath $args[0] -AclObject $acl`
	if output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", grant, path).CombinedOutput(); err != nil {
		t.Fatalf("seed Windows Agent ACL: %v: %s", err, output)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat Windows Agent directory: %v", err)
	}
	if err := validateHostAgentDirectoryPermissions(path, info); err != nil {
		t.Fatalf("harden Windows Agent ACL: %v", err)
	}
	const inspect = `$current = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$allowed = @($current, 'S-1-5-18', 'S-1-5-32-544')
$bad = @( (Get-Acl -LiteralPath $args[0]).Access | Where-Object { $_.AccessControlType -eq 'Allow' -and $allowed -notcontains $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value } )
Write-Output $bad.Count`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", inspect, path).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "0" {
		t.Fatalf("inspect hardened Windows Agent ACL: %v: %s", err, output)
	}
}
