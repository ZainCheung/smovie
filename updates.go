package main

import (
	_ "embed"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// The update window's icon: the app's, at the size the window shows.
//
//go:embed icon/update.png
var updateIcon []byte

// useUpdates has the app check the releases on GitHub for a newer version,
// a while after launch and then daily, and offer it in a window with its
// release notes: MyGo's updater, as Sparkle's. Call it before App.Run.
// Builds made without the update key, and `mygo dev`, never check.
func (a *app) useUpdates() {
	mygo.Use(native.New(updater.Options{Icon: updateIcon, Language: "zh-Hans"}))
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
		a.newVersion = updater.AvailableVersion()
	}
	read()
	updater.OnChange(func() {
		read()
		a.win.Invalidate()
	})
}

// aboutRows are the version, with the button that checks for a newer one,
// and whether to check in the background.
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
	// Builds that cannot update themselves, as the Debian package's, say
	// why in the window, and have nothing to check in the background.
	if a.updates && mygo.Updater.Enabled() {
		settingRow(c, "自动检查更新", "每天在后台检查一次，有新版本时提示安装", func() {
			if ui.Switch(c.Key("auto-update"), &a.autoUpdate).Label("自动检查更新").Changed() {
				updater.SetAutomaticChecks(a.autoUpdate)
			}
		})
	}
}
