// Command desktop is a thin native shell around the tokenwardend daemon
// (REQUIREMENTS.md NFR-UI-2): it spawns the daemon as a child process, waits
// for it to come up, and points a native window at its own HTTP address —
// the daemon's API and UI are otherwise untouched. It adds exactly what a
// terminal-launched daemon doesn't have: a system tray, a window that hides
// rather than quits (the whole point of the scheduler is unattended
// overnight dispatch, so closing the window must not kill it), and a real
// quit that stops the daemon cleanly.
//
// Deliberately its own Go module (see desktop/go.mod): Wails requires cgo to
// bind to the native webview, which conflicts with the root module's
// documented "no cgo, anywhere" constraint (CLAUDE.md) that keeps the
// daemon/CLI/probe cross-compilable with no C toolchain. Isolating this
// module keeps that constraint intact for the code it actually protects.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Wails still wants an embeddable frontend for its asset server even though
// this app never shows it — the window is redirected to the daemon's own
// URL before it's ever made visible (see main). Left as the scaffolded
// default rather than stripped out, so the standard `wails3 build`/Taskfile
// pipeline (which assumes a frontend/dist exists) needs no changes.
//
//go:embed all:frontend/dist
var frontendAssets embed.FS

//go:embed assets/appicon-1024.png
var appIconPNG []byte

//go:embed assets/tray-template.png
var trayTemplatePNG []byte

// daemonAddr mirrors internal/config.DefaultListenAddr. Duplicated rather
// than imported: this module is deliberately isolated from the root
// module's dependency graph (see the package doc), so it talks to the
// daemon over the same loopback HTTP API the web UI and CLI already use,
// rather than reaching into internal/ packages.
const daemonAddr = "http://127.0.0.1:7842"

func main() {
	daemonCmd, err := startDaemon()
	if err != nil {
		log.Fatalf("desktop: %v", err)
	}
	if err := waitForHealth(15 * time.Second); err != nil {
		log.Fatalf("desktop: %v", err)
	}

	var window *application.WebviewWindow

	app := application.New(application.Options{
		Name:        "tokenwarden",
		Description: "Budget-aware scheduler for Claude Code work",
		Icon:        appIconPNG,
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontendAssets),
		},
		// Stopping the daemon here (rather than relying on it to notice its
		// parent died) is what makes "Quit" actually free the port/database
		// instead of leaving an orphaned tokenwardend running.
		OnShutdown: func() {
			stopDaemon(daemonCmd)
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.tokenwarden.desktop",
			// A second launch attempt just refocuses the existing window —
			// it must not start a second daemon fighting over the port and
			// database.
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if window != nil {
					window.Show()
					window.Restore()
					window.Focus()
				}
			},
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "tokenwarden",
		Width:  1200,
		Height: 900,
		URL:    daemonAddr,
	})

	// Closing the window backgrounds the app rather than quitting it — the
	// scheduler needs to keep dispatching while nobody's looking at the
	// window. Only the tray's Quit (below) does a real shutdown.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetTooltip("tokenwarden")
	if runtime.GOOS == "darwin" {
		// A template icon (flat black-on-transparent) so macOS can recolor
		// it correctly for light/dark menu bars — the full-color app icon's
		// blurred gradient detail wouldn't work as a template mask.
		tray.SetTemplateIcon(trayTemplatePNG)
	} else {
		tray.SetIcon(appIconPNG)
	}

	menu := app.NewMenu()
	menu.Add("Open").OnClick(func(ctx *application.Context) {
		window.Show()
		window.Restore()
		window.Focus()
	})
	menu.AddSeparator()
	haltItem := menu.Add("Halt")
	haltItem.OnClick(func(ctx *application.Context) {
		halted, err := killSwitchHalted()
		if err != nil {
			return
		}
		_ = setKillSwitch(!halted)
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)

	go pollKillSwitchLabel(haltItem)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// findDaemonBinary locates tokenwardend: next to this executable first (the
// packaged-build layout, once a later slice bundles it there), then
// ../bin/tokenwardend relative to the working directory (the dev-mode
// layout — `go run .` from desktop/ against the repo's own `task build`
// output), then PATH.
func findDaemonBinary() (string, error) {
	name := "tokenwardend"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "..", "bin", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("could not locate %s (checked next to this executable, ../bin, and PATH)", name)
}

// startDaemon spawns tokenwardend as a child process. Its stdout/stderr are
// wired to this process's own so daemon logs are visible wherever the shell
// itself is running (a terminal in dev mode; Phase B will need to decide
// where a packaged build's logs should go).
func startDaemon() (*exec.Cmd, error) {
	bin, err := findDaemonBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	return cmd, nil
}

// waitForHealth blocks until GET /api/health responds or timeout elapses.
func waitForHealth(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(daemonAddr + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon at %s did not become healthy within %s", daemonAddr, timeout)
}

// stopDaemon asks the daemon to terminate gracefully (SIGTERM, so any
// in-flight dispatch's context gets cancelled the same way the kill switch
// already cancels it — see internal/dispatch.Dispatcher.Halt) and force-kills
// it if it doesn't exit promptly. Windows has no SIGTERM equivalent worth
// relying on here, so it goes straight to Kill.
func stopDaemon(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// killSwitchHalted and setKillSwitch talk to the daemon's own kill-switch
// API (GET/POST /api/kill-switch...) — the same endpoints the web UI and CLI
// use, so halting from the tray is indistinguishable from halting anywhere
// else (REQUIREMENTS.md FR-SAFE-4).
func killSwitchHalted() (bool, error) {
	resp, err := http.Get(daemonAddr + "/api/kill-switch")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var body struct {
		Halted bool `json:"halted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	return body.Halted, nil
}

func setKillSwitch(halt bool) error {
	path := "/api/kill-switch/resume"
	if halt {
		path = "/api/kill-switch/halt"
	}
	resp, err := http.Post(daemonAddr+path, "application/json", nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// pollKillSwitchLabel keeps the tray's Halt/Resume item honest about actual
// daemon state — same 5s-poll approach as ui/src/components/Layout.tsx's
// header badge, and for the same reason: the switch can be toggled from
// anywhere (the web UI, the CLI, another tray), so a label set once at
// startup would go stale immediately.
func pollKillSwitchLabel(item *application.MenuItem) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		halted, err := killSwitchHalted()
		if err != nil {
			continue
		}
		if halted {
			item.SetLabel("Resume")
		} else {
			item.SetLabel("Halt")
		}
	}
}
