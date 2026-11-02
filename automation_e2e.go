//go:build e2e

package main

// Built with -tags e2e, the app can be driven from outside through
// MYGO_AUTOMATION=<socket> (see mygo's automation plugin), and SIGUSR1
// saves the player page's screenshot to $SMOVIE_PAGE_SHOT, which the
// window's own capture leaves blank where the web view shows; SIGUSR2
// runs $SMOVIE_EVAL, or the script in $SMOVIE_EVAL_FILE, in the player page, or pauses it, which then shows its
// controls. SMOVIE_CONFIG names the directory of the history and settings.
import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/egoist/mygo/plugins/automation"
)

func init() {
	// SMOVIE_CONFIG keeps a test's history and settings apart from the
	// user's.
	if dir := os.Getenv("SMOVIE_CONFIG"); dir != "" {
		configOverride = dir
	}
	onReady = func(a *app) {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGUSR1, syscall.SIGUSR2)
		go func() {
			for sig := range ch {
				if sig == syscall.SIGUSR2 {
					script := os.Getenv("SMOVIE_EVAL")
					if f := os.Getenv("SMOVIE_EVAL_FILE"); f != "" {
						if b, err := os.ReadFile(f); err == nil {
							script = string(b)
						}
					}
					if script == "" {
						script = "smovie.pause()"
					}
					out, err := a.player.Page().Eval(script)
					_ = os.WriteFile(os.Getenv("SMOVIE_PAGE_SHOT")+".txt", []byte(fmt.Sprintf("%v %v", out, err)), 0o644)
					continue
				}
				if png, err := a.player.CapturePage(); err == nil {
					_ = os.WriteFile(os.Getenv("SMOVIE_PAGE_SHOT"), png, 0o644)
				}
			}
		}()
	}
}
