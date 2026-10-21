package desktopnotify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

type toastRunner struct {
	recordingRunner
	input string
	ctx   context.Context
	call  func(context.Context) error
}

func (r *toastRunner) RunInput(ctx context.Context, name, input string, args ...string) error {
	r.input, r.ctx = input, ctx
	_ = r.Run(ctx, name, args...)
	if r.call != nil {
		return r.call(ctx)
	}
	return r.err
}

func TestNewForPlatformWindows(t *testing.T) {
	n, supported := newForPlatform("windows", &toastRunner{})
	if _, ok := n.(*windowsNotifier); !supported || !ok {
		t.Fatalf("newForPlatform(windows) = (%T, %v)", n, supported)
	}
}

func TestWindowsToastDataTransport(t *testing.T) {
	run := &toastRunner{recordingRunner: recordingRunner{path: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}}
	executable := `C:\Users\通知 App\Goobers & 'test'\goobers.exe`
	n := &windowsNotifier{run: run, executable: func() (string, error) { return executable, nil }}
	message := Message{
		Title: "Goobers 日本語 😀 \" ' <>&",
		Body:  "line 1\r\nline 2 ` $(Stop-Process -Id $PID); & exit 99\n</text><audio src='bad'/>",
	}
	ctx := context.Background()
	if err := n.Notify(ctx, message); err != nil {
		t.Fatal(err)
	}
	if run.lookup != "powershell.exe" || run.name != run.path || run.ctx != ctx {
		t.Fatalf("incorrect executable discovery or context: %+v", run)
	}
	wantArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", encodedPowerShell(windowsToastScript)}
	if !reflect.DeepEqual(run.args, wantArgs) {
		t.Fatalf("unexpected invocation: %#v", run.args)
	}
	for _, arg := range run.args {
		if strings.Contains(arg, message.Title) || strings.Contains(arg, executable) {
			t.Fatal("notification data leaked into executable arguments")
		}
	}
	data, err := base64.StdEncoding.DecodeString(run.input)
	if err != nil {
		t.Fatal(err)
	}
	var payload toastPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Executable != executable {
		t.Fatalf("executable = %q", payload.Executable)
	}
	var document struct {
		Text  []string `xml:"visual>binding>text"`
		Audio []string `xml:"audio"`
	}
	if err := xml.Unmarshal([]byte(payload.XML), &document); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.Text, []string{message.Title, message.Body}) || len(document.Audio) != 0 {
		t.Fatalf("XML changed data or allowed injected nodes: %+v", document)
	}
	if !strings.Contains(payload.XML, `template="ToastGeneric"`) {
		t.Fatalf("missing toast template: %s", payload.XML)
	}
	firstArgs := append([]string(nil), run.args...)
	if err := n.Notify(ctx, Message{Title: "different", Body: "content"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstArgs, run.args) {
		t.Fatal("PowerShell script depends on notification content")
	}
}

func TestEncodedPowerShell(t *testing.T) {
	const script = "Write-Output '日本語 😀'"
	data, err := base64.StdEncoding.DecodeString(encodedPowerShell(script))
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[2*i:])
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Fatalf("decoded script = %q", got)
	}
}

type toastExit int

func (e toastExit) Error() string {
	return "untrusted diagnostic containing private notification content"
}
func (e toastExit) ExitCode() int { return int(e) }

func TestWindowsToastErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  toastRunner
		exe  func() (string, error)
		want string
	}{
		{name: "missing PowerShell", run: toastRunner{recordingRunner: recordingRunner{pathErr: errors.New("missing")}}, want: "powershell.exe"},
		{name: "missing executable", exe: func() (string, error) { return "", errors.New("missing") }, want: "cannot locate goobers.exe"},
		{name: "command failure", run: toastRunner{recordingRunner: recordingRunner{err: errors.New("private data")}}, want: "PowerShell failed"},
		{name: "prerequisites", run: toastRunner{recordingRunner: recordingRunner{err: toastExit(10)}}, want: "interactive Windows desktop"},
		{name: "registration", run: toastRunner{recordingRunner: recordingRunner{err: toastExit(11)}}, want: "per-user Start Menu"},
		{name: "disabled", run: toastRunner{recordingRunner: recordingRunner{err: toastExit(12)}}, want: "Settings > System > Notifications"},
		{name: "submission", run: toastRunner{recordingRunner: recordingRunner{err: toastExit(13)}}, want: "submission failed"},
		{name: "payload", run: toastRunner{recordingRunner: recordingRunner{err: toastExit(14)}}, want: "invalid toast payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.exe == nil {
				tc.exe = func() (string, error) { return `C:\Goobers\goobers.exe`, nil }
			}
			n := &windowsNotifier{run: &tc.run, executable: tc.exe}
			err := n.Notify(context.Background(), Message{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(err.Error()) > 250 || strings.Contains(err.Error(), "private") {
				t.Fatalf("Notify error = %v, want bounded error containing %q", err, tc.want)
			}
		})
	}
}

func TestWindowsToastCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &toastRunner{call: func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return errors.New("process terminated")
	}}
	n := &windowsNotifier{run: run, executable: func() (string, error) { return `C:\goobers.exe`, nil }}
	if err := n.Notify(ctx, Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Notify error = %v, want context.Canceled", err)
	}
	run.name = ""
	if err := n.Notify(ctx, Message{}); !errors.Is(err, context.Canceled) || run.name != "" {
		t.Fatalf("already canceled notification invoked command: %v", err)
	}
}
