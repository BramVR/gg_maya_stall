//go:build windows

package cli

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

func validateHostAgentDirectoryPermissions(path string, _ fs.FileInfo) error {
	if isHostAgentFilesystemRoot(path) {
		return fmt.Errorf("Windows Host Agent work root must be a dedicated non-root directory")
	}
	const script = `$ErrorActionPreference = 'Stop'
$path = $args[0]
$current = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetOwner($current)
$acl.SetAccessRuleProtection($true, $false)
$inheritance = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
$propagation = [System.Security.AccessControl.PropagationFlags]::None
$rights = [System.Security.AccessControl.FileSystemRights]::FullControl
$allow = [System.Security.AccessControl.AccessControlType]::Allow
$system = New-Object System.Security.Principal.SecurityIdentifier -ArgumentList 'S-1-5-18'
$administrators = New-Object System.Security.Principal.SecurityIdentifier -ArgumentList 'S-1-5-32-544'
foreach ($sid in @($current, $system, $administrators)) {
  $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($sid, $rights, $inheritance, $propagation, $allow)))
}
Set-Acl -LiteralPath $path -AclObject $acl
$allowed = @($current.Value, 'S-1-5-18', 'S-1-5-32-544')
$actual = Get-Acl -LiteralPath $path
$bad = @($actual.Access | Where-Object { $_.AccessControlType -eq $allow -and $allowed -notcontains $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value })
if ($bad.Count -ne 0) { throw 'Agent work root grants access to another identity' }
`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, path)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("enforce private Windows Host Agent ACL for %s: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func isHostAgentFilesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	return filepath.Dir(clean) == clean || volume != "" && strings.EqualFold(clean, volume+string(filepath.Separator))
}
