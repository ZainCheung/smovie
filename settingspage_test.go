package main

import (
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The full screen panel is an experiment on macOS, off until switched
// on in the settings; Windows and Linux always have it.
func TestPanelFullScreenSetting(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	a.images = newImageCache(func() {})
	a.server = &mediaServer{}
	a.server.hostFS.Store(a.hostFullScreen())
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Scroll(c).Fill().Children(func() { a.settingsPage(c) })
		a.syncSettings()
	}, 1280, 1400)
	tt.Frame()
	if runtime.GOOS != "darwin" {
		if tt.HasText("全屏剧集面板") || !a.hostFullScreen() {
			t.Fatalf("the panel is not simply on: texts %v", tt.Texts())
		}
		return
	}
	if !tt.HasText("实验性功能") || !tt.HasText("全屏剧集面板") {
		t.Fatalf("no setting in %v", tt.Texts())
	}
	if a.hostFullScreen() || a.server.hostFS.Load() {
		t.Fatal("on by default")
	}
	// The switch stands right of the row's title and description.
	title, _ := tt.Find("全屏剧集面板")
	desc, ok := tt.Find(panelFullDetail)
	if !ok {
		t.Fatal("no description")
	}
	tt.ClickAt(desc.X+desc.W+20+18, (title.Y+desc.Y+desc.H)/2)
	tt.Frame()
	tt.Frame()
	if !a.panelFull || !a.hostFullScreen() || !a.server.hostFS.Load() {
		t.Fatalf("not switched on: setting %v, server %v", a.panelFull, a.server.hostFS.Load())
	}
	time.Sleep(100 * time.Millisecond) // saved off the main thread
	if !loadSettings().PanelFullScreen {
		t.Fatal("not saved")
	}
}
