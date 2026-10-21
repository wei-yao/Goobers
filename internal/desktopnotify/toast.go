package desktopnotify

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"unicode/utf16"
)

//go:embed toast.ps1
var windowsToastScript string

type windowsNotifier struct {
	run        commandRunner
	executable func() (string, error)
}

type toastPayload struct {
	Executable string
	XML        string
}

func encodeToastPayload(executable string, message Message) (string, error) {
	type binding struct {
		Template string   `xml:"template,attr"`
		Text     []string `xml:"text"`
	}
	document := struct {
		XMLName xml.Name `xml:"toast"`
		Binding binding  `xml:"visual>binding"`
	}{Binding: binding{Template: "ToastGeneric", Text: []string{message.Title, message.Body}}}
	content, err := xml.Marshal(document)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(toastPayload{Executable: executable, XML: string(content)})
	if err != nil {
		return "", err
	}
	// ASCII stdin avoids Windows PowerShell's console code-page conversions.
	return base64.StdEncoding.EncodeToString(payload), nil
}

func encodedPowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func (n *windowsNotifier) Notify(ctx context.Context, message Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("desktop notification: %w", err)
	}
	powershell, err := n.run.LookPath("powershell.exe")
	if err != nil {
		return errors.New("desktop notification: Windows PowerShell 5.1 (powershell.exe) is required on PATH")
	}
	executable, err := n.executable()
	if err != nil {
		return errors.New("desktop notification: cannot locate goobers.exe for Start Menu registration")
	}
	payload, err := encodeToastPayload(executable, message)
	if err != nil {
		return errors.New("desktop notification: cannot encode toast content")
	}
	err = n.run.RunInput(ctx, powershell, payload,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", encodedPowerShell(windowsToastScript))
	if err == nil {
		// Submission is not proof of display: Windows can suppress a toast.
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("desktop notification: %w", ctx.Err())
	}
	detail := "PowerShell failed; check Windows PowerShell 5.1 and application-control policy"
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 10:
			detail = "requires a signed-in interactive Windows desktop, Windows PowerShell 5.1, and WinRT toast APIs"
		case 11:
			detail = "cannot register Goobers in the per-user Start Menu; check profile permissions and PowerShell Add-Type policy"
		case 12:
			detail = "Windows notifications are disabled for Goobers, this user, or by policy; check Settings > System > Notifications"
		case 13:
			detail = "WinRT toast submission failed; check the Windows notification service and desktop session"
		case 14:
			detail = "invalid toast payload"
		}
	}
	// Never echo PowerShell diagnostics, which may contain notification content.
	return fmt.Errorf("desktop notification: %s", detail)
}
