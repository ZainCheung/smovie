//go:build e2e

package main

// Built with -tags e2e, the app can be driven from outside through
// MYGO_AUTOMATION=<socket> (see mygo's automation plugin), and SIGUSR1
// saves the player page's screenshot to $SMOVIE_PAGE_SHOT, which the
// window's own capture leaves blank where the web view shows; SIGUSR2
// pauses the player, which then shows its controls.
import (
	"os"
	"os/signal"
	"syscall"

	_ "github.com/egoist/mygo/plugins/automation"
)

func init() {
	onReady = func(a *app) {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGUSR1, syscall.SIGUSR2)
		go func() {
			for sig := range ch {
				if sig == syscall.SIGUSR2 {
					_, _ = a.player.Page().Eval("smovie.pause()")
					continue
				}
				if png, err := a.player.CapturePage(); err == nil {
					_ = os.WriteFile(os.Getenv("SMOVIE_PAGE_SHOT"), png, 0o644)
				}
			}
		}()
	}
}
