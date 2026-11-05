package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

const epBlock = 50 // episodes per range of the list

// watchPage shows the player sized to the episode's shape: a landscape
// video gets the width of the page with the series and its episodes
// below, a portrait one stands at the left with them beside it.
// playerKeys are the player's keys the app passes on while its own UI
// has the keyboard, as the web view has it only once clicked.
var playerKeys = []struct {
	mods ui.Modifiers
	key  ui.Key
	name string
}{
	{0, ui.KeySpace, " "}, {0, ui.KeyLeft, "ArrowLeft"}, {0, ui.KeyRight, "ArrowRight"},
	{0, ui.KeyUp, "ArrowUp"}, {0, ui.KeyDown, "ArrowDown"}, {0, ui.KeyM, "m"}, {0, ui.KeyF, "f"}, {0, ui.KeyD, "d"},
	{0, ui.KeyBracketLeft, "["}, {0, ui.KeyBracketRight, "]"}, {0, ui.KeyMinus, "-"}, {0, ui.KeyEqual, "="},
	{ui.Shift, ui.KeyLeft, "Shift+ArrowLeft"}, {ui.Shift, ui.KeyRight, "Shift+ArrowRight"},
}

// The watch page is laid out as video sites have it: the player at the
// top of a main column, with the episode's title, the series' actions,
// its description and the comments below; and beside it a column with
// the episodes and the series to watch next, which goes under the
// comments in a narrow window.
const (
	watchPad  = 24
	sideWidth = 400
	watchGap  = 24
	wideWatch = 1080 // the narrowest window with the side column
)

func (a *app) watchPage(c *ui.Context) {
	s := a.series
	if s == nil {
		return
	}
	if !a.detailsOpen {
		for _, k := range playerKeys {
			if c.Shortcut(k.mods, k.key) {
				a.tellPlayer("smovie.key", k.name)
				// The page takes the keys from now on: a held key's repeats and
				// its release, which hold → at 2x, reach it only there.
				a.player.Focus()
			}
		}
	}
	if a.detail != nil {
		s = &a.detail.Series
	}
	w, h := c.Size()
	wide := w >= wideWatch
	mainW := w - 2*watchPad
	if wide {
		mainW -= sideWidth + watchGap
	}
	// A 16:9 frame, as the site's videos are, short enough to leave the
	// title in sight; a portrait episode stands in its middle.
	playerH := min(mainW*9/16, max(h-navHeight-150, 300))

	ui.Row(c).Padding(navHeight+8, watchPad, 48, watchPad).Gap(watchGap).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Width(mainW).Shrink(0).Gap(14).Children(func() {
			ui.WebView(c, a.player).Width(mainW).Height(playerH).Radius(12).Background(rgba(0, 0, 0, 255)).
				Children(func() { a.playerOverlay(c) })
			a.watchHeader(c, *s)
			if !wide {
				a.episodePanel(c, *s, mainW, 0)
			}
			a.description(c, *s)
			a.comments(c, *s)
			if !wide {
				a.recommended(c, *s, mainW)
			}
		})
		if wide {
			ui.Column(c).Width(sideWidth).Shrink(0).Gap(20).Children(func() {
				a.episodePanel(c, *s, sideWidth, 0)
				a.recommended(c, *s, sideWidth)
			})
		}
	})
}

// fullPanelWidth is the panel beside the player in full screen, as
// video sites open one from the title over their player.
const fullPanelWidth = 448

// fullPlayer is the player alone, filling the window in full screen, and
// beside it, once its title over the player is clicked, a panel with the
// episode, its series and the episodes to change to. Escape closes the
// panel, then leaves; F leaves, as the page's keys do once it has the
// keyboard.
func (a *app) fullPlayer(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) && a.fullPanel {
		a.setFullPanel(false)
	} else if c.Shortcut(0, ui.KeyEscape) || c.Shortcut(0, ui.KeyF) {
		a.setPlayerFull(false)
		return
	}
	for _, k := range playerKeys {
		if k.key != ui.KeyF && c.Shortcut(k.mods, k.key) {
			a.tellPlayer("smovie.key", k.name)
			a.player.Focus()
		}
	}
	_, h := c.Size()
	ui.Row(c).Fill().Background(rgba(0, 0, 0, 255)).Children(func() {
		ui.WebView(c, a.player).Grow(1).MinWidth(0).Height(h).Background(rgba(0, 0, 0, 255))
		// The panel slides in from the right, pushing the picture to the
		// middle of what is left; its content keeps its width meanwhile.
		box := ui.Box(c.Key("full-panel")).Height(h).Shrink(0).Clip().Background(colorBG)
		w := box.Animate("w", map[bool]float32{true: fullPanelWidth, false: 0}[a.fullPanel], 220*time.Millisecond)
		box.Width(w)
		if w < 1 {
			return
		}
		box.Children(func() { a.fullPanelContent(c, h) })
	})
}

// fullPanelContent is the episode's title and series, as on the watch
// page, over the episodes and the series' seasons.
func (a *app) fullPanelContent(c *ui.Context, h float32) {
	s := a.series
	if a.detail != nil {
		s = &a.detail.Series
	}
	if s == nil {
		return
	}
	const pad = 20
	inner := float32(fullPanelWidth - 2*16 - 2*pad)
	ui.Column(c).Absolute().Left(0).Top(0).Width(fullPanelWidth).Height(h).Padding(16).Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).Radius(16).Clip().Border(1, rgba(255, 255, 255, 34)).Background(rgba(255, 255, 255, 8)).Children(func() {
			ui.Scroll(c.Key("full-panel-scroll")).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(pad).Gap(16).Children(func() {
					ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
						ui.Text(c, a.episodeTitle(*s)).Grow(1).MinWidth(0).FontSize(22).FontWeight(700).LineHeight(1.3).MaxLines(3).Selectable()
						x := ui.ButtonBase(c.Key("full-panel-close")).Size(36, 36).Shrink(0).Radius(18).Center().Label("关闭")
						x.Background(rgba(255, 255, 255, 30*b2f(x.Hovered())))
						x.Children(func() { ui.Icon(c, iconClose).Size(22, 22).TextColor(colorWhite) })
						if x.Clicked() {
							a.setFullPanel(false)
						}
					})
					a.seriesLine(c, *s, inner-40-12-12-120)
					// The grid takes the height the title, the series and the
					// panel's head and ranges leave.
					a.episodePanel(c, *s, inner, h-400)
				})
			})
		})
	})
}

func (a *app) episodeTile(c *ui.Context, i int, width float32) {
	cur := i == a.current
	ep := ui.ButtonBase(c.Key(i)).Size(width, 40).Radius(6).Center().Label(fmt.Sprintf("第 %d 集", i+1))
	hover := ep.Hovered()
	switch {
	case cur:
		ep.Background(colorRed)
	case hover:
		ep.Background(rgba(255, 255, 255, 46)).Border(1, withAlpha(colorWhite, 120))
	default:
		ep.Background(rgba(255, 255, 255, 20))
	}
	ep.Children(func() {
		if cur && a.job != nil && a.job.Playable() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				equalizer(c)
				ui.Textf(c, "%d", i+1).FontSize(14).FontWeight(700)
			})
		} else {
			if cur {
				c.After(250 * time.Millisecond) // until the episode is ready
			}
			ui.Textf(c, "%d", i+1).FontSize(14).FontWeight(map[bool]int{true: 700, false: 500}[cur]).
				TextColor(map[bool]ui.Color{true: colorWhite, false: rgba(220, 220, 220, 255)}[cur || hover])
		}
	})
	if ep.Clicked() && !cur {
		a.play(i)
	}
}

// equalizer is three bars bouncing, marking the episode that plays.
func equalizer(c *ui.Context) {
	ui.Row(c).Gap(2).AlignItems(ui.End).Height(12).Children(func() {
		for b := range 3 {
			bar := ui.Box(c.Key(fmt.Sprint("eq", b))).Width(2).Radius(1).Background(colorWhite)
			p := bar.Loop("eq", time.Duration(500+b*170)*time.Millisecond, ui.EaseInOut)
			if p > 0.5 {
				p = 1 - p
			}
			bar.Height(3 + 18*p)
		}
	})
}

// playerOverlay covers the web view until the series' episodes are known;
// from then on the player page shows its own progress.
func (a *app) playerOverlay(c *ui.Context) {
	if a.detail != nil {
		return
	}
	white := rgba(255, 255, 255, 235)
	ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Radius(12).Clip().Background(rgba(0, 0, 0, 255)).Children(func() {
		if a.series != nil {
			if pic := a.images.Get(c, a.series.Cover, imgSmall, true); pic.Img != nil {
				ui.Image(c, pic.Img).Fill().Fit(ui.Cover).Blur(24).Opacity(0.35)
			}
		}
		ui.Column(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Center().Gap(12).Padding(28).Children(func() {
			if a.detailErr != "" {
				ui.Text(c, a.detailErr).TextColor(white).TextAlign(ui.Center)
				return
			}
			ui.Spinner(c).TextColor(white).FontSize(26)
			ui.Text(c, "正在加载剧集…").TextColor(rgba(255, 255, 255, 150)).FontSize(13)
		})
	})
}
