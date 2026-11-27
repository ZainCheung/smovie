package main

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// The update window's icon: the app's, at the size the window shows.
//
//go:embed icon/update.png
var updateIcon []byte

// appUpdate represents an available update that can be installed silently in
// the background and applied on relaunch.
type appUpdate struct {
	Version string
	Install func(ctx context.Context, progress func(downloaded, total int64)) error
}

// Replaceable in tests.
var (
	updaterEnabled = func() bool { return mygo.Updater.Enabled() }
	checkAppUpdate = func(ctx context.Context) (*appUpdate, error) {
		up, err := mygo.Updater.Check(ctx)
		if err != nil || up == nil {
			return nil, err
		}
		return &appUpdate{
			Version: up.Version,
			Install: up.Install,
		}, nil
	}
	relaunchApp = func() { mygo.App.Relaunch() }
)

// useUpdates wires MyGo's updater plugin for manual checks from the menu bar
// and settings page while disabling its automatic popup window so launch checks
// surface through the subtle top-bar download button instead.
func (a *app) useUpdates() {
	mygo.Use(native.New(updater.Options{
		Icon:                   updateIcon,
		Language:               "zh-Hans",
		DisableAutomaticChecks: true,
	}))
	a.updates = true
	// Only macOS has a menu bar by default: its own, with the item where
	// Mac apps have it.
	if runtime.GOOS == "darwin" {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: mygo.App.Name(), Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				updater.MenuItem(),
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleFileMenu},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))
	}
}

// watchUpdates keeps what the settings page shows of the updater. It runs
// on the main thread, once the window exists.
func (a *app) watchUpdates() {
	if !a.updates {
		return
	}
	read := func() {
		a.autoUpdate = updater.AutomaticChecks()
		if v := updater.AvailableVersion(); v != "" {
			a.newVersion = v
		}
	}
	read()
	updater.OnChange(func() {
		read()
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

// checkForUpdateOnLaunch checks for a newer release once in the background when
// the app opens. When an update is found, the top-bar download button appears.
func (a *app) checkForUpdateOnLaunch() {
	if !updaterEnabled() {
		return
	}
	win := a.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		up, err := checkAppUpdate(ctx)
		if err != nil || up == nil {
			return
		}
		apply := func() {
			a.pendingUpdate = up
			if up.Version != "" {
				a.newVersion = up.Version
			}
		}
		if win != nil {
			win.Update(apply)
		} else {
			apply()
		}
	}()
}

// startUpdateInstall downloads and stages the available update in the
// background, then relaunches the app to finish installing it.
func (a *app) startUpdateInstall() {
	if a.updateDownloading || a.pendingUpdate == nil || a.pendingUpdate.Install == nil {
		return
	}
	up := a.pendingUpdate
	a.updateDownloading = true
	a.updateProgress = 0
	win := a.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		var lastReport atomic.Int64
		err := up.Install(ctx, func(downloaded, total int64) {
			if total <= 0 {
				return
			}
			now := time.Now().UnixMilli()
			prev := lastReport.Load()
			if downloaded < total && now-prev < 120 {
				return
			}
			if !lastReport.CompareAndSwap(prev, now) && downloaded < total {
				return
			}
			p := float32(downloaded) / float32(total)
			if p < 0 {
				p = 0
			} else if p > 1 {
				p = 1
			}
			if win != nil {
				win.Update(func() { a.updateProgress = p })
			}
		})
		if err != nil {
			fail := func() {
				a.updateDownloading = false
				a.updateProgress = 0
				a.pendingToast = "更新下载失败，请稍后重试"
			}
			if win != nil {
				win.Update(fail)
			} else {
				fail()
			}
			return
		}
		finish := func() {
			a.updateProgress = 1
			relaunchApp()
		}
		if win != nil {
			win.Update(finish)
		} else {
			finish()
		}
	}()
}

// navUpdateButton renders the leftmost toolbar update control when a new
// release is available: a download icon at rest, morphing into a red "更新"
// text pill on hover, and a spinning progress indicator while downloading.
func (a *app) navUpdateButton(c *ui.Context) {
	if a.pendingUpdate == nil && !a.updateDownloading {
		return
	}
	if a.updateDownloading {
		tip := "正在下载更新…"
		if pct := int(a.updateProgress * 100); pct > 0 && pct < 100 {
			tip = fmt.Sprintf("正在下载更新 %d%%…", pct)
		}
		btn := ui.ButtonBase(c.Key("nav-update")).Size(32, 32).Radius(16).Center().
			Disabled(true).Label("正在下载更新").Tooltip(tip)
		btn.Background(withAlpha(colorRed, 48)).Border(1, withAlpha(colorRed, 140))
		btn.Children(func() {
			ui.Spinner(c).FontSize(16).TextColor(colorWhite).Label("正在下载更新")
		})
		return
	}

	tip := "更新应用"
	if a.pendingUpdate.Version != "" {
		tip = "更新到 " + a.pendingUpdate.Version
	}
	btn := ui.ButtonBase(c.Key("nav-update")).Height(32).Radius(16).Center().Label("更新").Tooltip(tip)
	hover := btn.Hovered()
	if hover {
		btn.Padding(0, 14).Background(colorRed)
		btn.Children(func() {
			ui.Text(c, "更新").FontSize(13).FontWeight(700).TextColor(colorWhite).SingleLine()
		})
	} else {
		btn.Width(32).Background(withAlpha(colorWhite, 22))
		btn.Children(func() {
			ui.Icon(c, iconDownload).Size(18, 18).TextColor(colorWhite)
		})
	}
	if btn.Clicked() {
		a.startUpdateInstall()
	}
}

// aboutRows are the version, with the button that checks for a newer one.
func (a *app) aboutRows(c *ui.Context) {
	detail := "内容来自红果短剧公开目录"
	if v := mygo.App.Version(); v != "" {
		detail = "版本 " + v + " · " + detail
	}
	if a.newVersion != "" {
		detail = "新版本 " + a.newVersion + " 可用 · " + detail
	}
	settingRow(c, "红果短剧", detail, func() {
		if a.updates && ui.Button(c.Key("check-updates"), "检查更新").Clicked() {
			updater.CheckForUpdates()
		}
	})
}
