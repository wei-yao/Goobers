package desktopnotify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runWindowsTestScript(t *testing.T, script string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", encodedPowerShell(script))
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(data))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Windows test script: %v\n%s", err, output)
	}
}

const readTestPayload = `
$ErrorActionPreference = 'Stop'
$payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())) | ConvertFrom-Json
`

func TestWindowsPowerShellSyntaxAndPayload(t *testing.T) {
	message := Message{Title: "日本語 😀 <>&\"'", Body: "new\nline\r\n$(throw 'not code'); ` &"}
	input, err := encodeToastPayload(`C:\Goobers App\goobers.exe`, message)
	if err != nil {
		t.Fatal(err)
	}
	runWindowsTestScript(t, readTestPayload+`
$tokens = $null
$parseErrors = $null
$null = [Management.Automation.Language.Parser]::ParseInput($payload.Script, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw 'Invalid production script syntax' }
$data = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($payload.Input)) | ConvertFrom-Json
$xml = New-Object System.Xml.XmlDocument
$xml.LoadXml($data.XML)
$texts = $xml.GetElementsByTagName('text')
if ($texts.Count -ne 2 -or $texts[0].InnerText -cne $payload.Title -or $texts[1].InnerText -cne $payload.Body) {
    throw 'Toast content did not survive transport'
}
`, map[string]string{"Script": windowsToastScript, "Input": input, "Title": message.Title, "Body": message.Body})
}

func TestWindowsShortcutRegistration(t *testing.T) {
	// Only the function definitions are evaluated: no real Start Menu or toast.
	definitions, _, ok := strings.Cut(windowsToastScript, "\ntry {\n    if ($PSVersionTable")
	if !ok {
		t.Fatal("cannot isolate registration definitions")
	}
	root := filepath.Join(t.TempDir(), "Goobers 日本語 & ' [test]")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goobers.exe", "moved goobers.exe", "not a directory"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runWindowsTestScript(t, definitions+readTestPayload+`
$programs = [IO.Path]::Combine($payload.Root, 'Programs')
$executable = [IO.Path]::Combine($payload.Root, 'goobers.exe')
Register-GoobersShortcut $executable $programs
$shortcut = [IO.Path]::Combine($programs, 'Goobers.lnk')
$stamp = [IO.File]::GetLastWriteTimeUtc($shortcut)
Register-GoobersShortcut $executable $programs
if ([IO.File]::GetLastWriteTimeUtc($shortcut) -ne $stamp) { throw 'Registration was not idempotent' }
$item = (New-Object -ComObject Shell.Application).NameSpace($programs).ParseName('Goobers.lnk')
if ($item.ExtendedProperty('System.AppUserModel.ID') -cne 'Goobers.Desktop') { throw 'Incorrect AppUserModelID' }
[GoobersShortcutIdentity]::Set($shortcut, 'Wrong.App')
Register-GoobersShortcut $executable $programs
$item = (New-Object -ComObject Shell.Application).NameSpace($programs).ParseName('Goobers.lnk')
if ($item.ExtendedProperty('System.AppUserModel.ID') -cne 'Goobers.Desktop') { throw 'Stale identity was not repaired' }
$moved = [IO.Path]::Combine($payload.Root, 'moved goobers.exe')
Register-GoobersShortcut $moved $programs
$link = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcut)
if ($link.TargetPath -ne $moved -or $link.Arguments -ne '') { throw 'Incorrect repaired shortcut target' }
$failed = $false
try { Register-GoobersShortcut $executable ([IO.Path]::Combine($payload.Root, 'not a directory')) } catch { $failed = $true }
if (-not $failed) { throw 'Registration failure was ignored' }
if (@([IO.Directory]::GetFiles($programs, '*.lnk')).Count -ne 1) { throw 'Temporary shortcuts were not cleaned up' }
`, map[string]string{"Root": root})
}

func TestWindowsExecRunnerCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := (execRunner{}).RunInput(ctx, "powershell.exe", "", "-NoLogo", "-NoProfile", "-NonInteractive",
		"-EncodedCommand", encodedPowerShell("[Threading.Thread]::Sleep(30000)"))
	if err == nil || ctx.Err() == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("PowerShell was not canceled promptly: %v", err)
	}
}

func TestWindowsVisibleToast(t *testing.T) {
	executable := os.Getenv("GOOBERS_TEST_WINDOWS_TOAST_EXE")
	if executable == "" {
		t.Skip("opt-in desktop smoke test: set GOOBERS_TEST_WINDOWS_TOAST_EXE to the installed goobers.exe")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("GOOBERS_TEST_WINDOWS_TOAST_EXE must be an absolute path")
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n := &windowsNotifier{run: execRunner{}, executable: func() (string, error) { return executable, nil }}
	if err := n.Notify(ctx, Message{
		Title: "Goobers desktop smoke test",
		Body:  "Goobers toast: 日本語 😀\nQuotes \"'<>& and $(text) are data.",
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("Toast submitted, NOT verified visible: confirm Goobers attribution and the fixed text on the signed-in desktop.")
}
