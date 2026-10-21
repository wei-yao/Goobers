// Package desktopnotify delivers best-effort native desktop notifications.
package desktopnotify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Message is the native notification content.
type Message struct {
	Title string
	Body  string
}

// Notifier delivers a desktop notification.
type Notifier interface {
	Notify(context.Context, Message) error
}

type commandRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
	RunInput(context.Context, string, string, ...string) error
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func (execRunner) RunInput(ctx context.Context, name, input string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Run()
}

type macOSNotifier struct {
	run commandRunner
}

type linuxNotifier struct {
	executable string
	run        commandRunner
}

// NewNative returns the notifier for the current platform. The boolean is
// false when the platform has no implementation.
func NewNative() (Notifier, bool) {
	return newForPlatform(runtime.GOOS, execRunner{})
}

func newForPlatform(platform string, run commandRunner) (Notifier, bool) {
	switch platform {
	case "darwin":
		return &macOSNotifier{run: run}, true
	case "linux":
		executable, err := run.LookPath("notify-send")
		if err != nil {
			return nil, false
		}
		return &linuxNotifier{executable: executable, run: run}, true
	case "windows":
		return &windowsNotifier{run: run, executable: os.Executable}, true
	default:
		return nil, false
	}
}

func (n *macOSNotifier) Notify(ctx context.Context, message Message) error {
	script := fmt.Sprintf(
		`display notification "%s" with title "%s"`,
		escapeAppleScriptString(message.Body),
		escapeAppleScriptString(message.Title),
	)
	if err := n.run.Run(ctx, "osascript", "-e", script); err != nil {
		return fmt.Errorf("desktop notification: %w", err)
	}
	return nil
}

func (n *linuxNotifier) Notify(ctx context.Context, message Message) error {
	if err := n.run.Run(ctx, n.executable, message.Title, message.Body); err != nil {
		return fmt.Errorf("desktop notification: %w", err)
	}
	return nil
}

func escapeAppleScriptString(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\r", "",
		"\n", `\n`,
	).Replace(value)
}
