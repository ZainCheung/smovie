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
	{0, ui.KeyUp, "ArrowUp"}, {0, ui.KeyDown, "ArrowDown"}, {0, ui.KeyM, "m"}, {0, ui.KeyF, "f"},
	{0, ui.KeyBracketLeft, "["}, {0, ui.KeyBracketRight, "]"}, {0, ui.KeyMinus, "-"}, {0, ui.KeyEqual, "="},
	{ui.Shift, ui.KeyLeft, "Shift+ArrowLeft"}, {ui.Shift, ui.KeyRight, "Shift+ArrowRight"},
}

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
	backdrop := a.images.Get(c, s.Cover, imgSmall, true)
	ratio := a.aspect()
	landscape := ratio >= 1

	contentW := w - 2*pagePad
	var playerW, playerH float32
	if landscape {
		playerH = min(contentW/ratio, max(h-navHeight-120, 320))
		playerW = playerH * ratio
	} else {
		playerH = max(h-navHeight-40, 360)
		playerW = min(playerH*ratio, w*0.42)
		playerH = playerW / ratio
	}

	const backdropH = 520
	ui.Column(c).Children(func() {
		// The poster, blurred, behind the top of the page. Elements placed
		// with Absolute paint over their siblings in the flow, so the
		// backdrop keeps them in a box of its own and the content follows
		// it, pulled up over it.
		ui.Box(c).Height(backdropH).Clip().Children(func() {
			if backdrop.Img != nil {
				ui.Image(c, backdrop.Img).Absolute().Left(-60).Top(-60).Size(w+120, backdropH+80).Fit(ui.Cover).Blur(48).Opacity(0.45 * backdrop.Fade)
			}
			ui.Box(c).Absolute().Left(0).Top(0).Size(w, backdropH).LinearGradient(ui.LinearGradient{
				Angle: 180, From: withAlpha(colorBG, 60), To: colorBG})
		})
		player := func() {
			ui.WebView(c, a.player).Width(playerW).Height(playerH).Shrink(0).Radius(10).
				Background(rgba(0, 0, 0, 255)).Shadow(0, 20, 60, 0, rgba(0, 0, 0, 160)).
				Children(func() { a.playerOverlay(c) })
		}
		if landscape {
			ui.Column(c).Margin(-backdropH, 0, 0, 0).Padding(navHeight+8, pagePad, 40, pagePad).Gap(28).Children(func() {
				ui.Row(c).Justify(ui.Center).Children(player)
				ui.Column(c).Gap(16).Children(func() { a.watchInfo(c, true) })
			})
		} else {
			ui.Row(c).Margin(-backdropH, 0, 0, 0).Padding(navHeight+8, pagePad, 40, pagePad).Gap(36).AlignItems(ui.Start).Children(func() {
				player()
				ui.Column(c).Grow(1).MinWidth(0).Gap(16).Padding(8, 0, 0, 0).Children(func() { a.watchInfo(c, false) })
			})
		}
	})
}

// fullPlayer is the player alone, filling the window in full screen.
// Escape and F leave it, as the page's do once it has the keyboard.
func (a *app) fullPlayer(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) || c.Shortcut(0, ui.KeyF) {
		a.setPlayerFull(false)
		return
	}
	for _, k := range playerKeys {
		if k.key != ui.KeyF && c.Shortcut(k.mods, k.key) {
			a.tellPlayer("smovie.key", k.name)
			a.player.Focus()
		}
	}
	ui.WebView(c, a.player).Fill().Background(rgba(0, 0, 0, 255))
}

func (a *app) watchInfo(c *ui.Context, wide bool) {
	s := *a.series
	if a.detail != nil {
		s = a.detail.Series
	}
	titleSize := float32(34)
	if wide {
		titleSize = 30
	}
	ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(14).Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.Title).FontSize(titleSize).FontWeight(800).LineHeight(1.2).Selectable().Shrink(1).MinWidth(0)
				a.myListButton(c, "watch-list", s, 36)
			})
			metaRow(c, s, 13)
			if s.Intro != "" {
				ui.Text(c, s.Intro).FontSize(14).TextColor(rgba(215, 215, 215, 255)).LineHeight(1.6).MaxWidth(820).Selectable()
			}
		})
	})
	if a.detail == nil {
		if a.detailErr != "" {
			ui.Text(c, a.detailErr).TextColor(colorRed)
		}
		return
	}
	n := len(a.detail.VIDs)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Margin(4, 0, 0, 0).Wrap().Children(func() {
		if a.current > 0 && bigButton(c, "prev", iconPrev, "上一集", false).Clicked() {
			a.play(a.current - 1)
		}
		if a.current+1 < n && bigButton(c, "next", iconNext, "下一集", true).Clicked() {
			a.play(a.current + 1)
		}
		ui.Spacer(c)
		ui.Text(c, "自动连播").FontSize(13).TextColor(colorMuted)
		ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
	})
	ui.Text(c, "快捷键：空格 播放/暂停 · ←/→ 快退/快进 5 秒 · 长按 → 2 倍速 · ↑/↓ 音量 · M 静音 · [ / ] 倍速 · F 全屏 · Shift+←/→ 上/下一集").
		FontSize(12).TextColor(colorFaint)
	ui.Box(c).Height(1).Background(rgba(255, 255, 255, 30)).Margin(10, 0, 4, 0)
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "选集").FontSize(20).FontWeight(700)
		if a.current >= 0 {
			ui.Textf(c, "正在播放第 %d 集 · 共 %d 集", a.current+1, n).FontSize(13).TextColor(colorFaint)
		}
	})
	if blocks := (n + epBlock - 1) / epBlock; blocks > 1 {
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for b := range blocks {
				lo, hi := b*epBlock+1, min((b+1)*epBlock, n)
				pill := ui.ButtonBase(c.Key(fmt.Sprint("blk", b))).Padding(5, 14).Radius(14).Label(fmt.Sprintf("%d-%d", lo, hi))
				active := b == a.epRange
				switch {
				case active:
					pill.Background(colorWhite)
				case pill.Hovered():
					pill.Background(rgba(255, 255, 255, 40))
				default:
					pill.Background(rgba(255, 255, 255, 18))
				}
				pill.Children(func() {
					ui.Textf(c, "%d-%d", lo, hi).FontSize(12).FontWeight(600).
						TextColor(map[bool]ui.Color{true: rgba(0, 0, 0, 255), false: rgba(220, 220, 220, 255)}[active])
				})
				if pill.Clicked() {
					a.epRange = b
				}
			}
		})
	}
	lo, hi := a.epRange*epBlock, min((a.epRange+1)*epBlock, n)
	ui.Row(c).Wrap().Gap(8).Children(func() {
		for i := lo; i < hi; i++ {
			a.episodeTile(c, i)
		}
	})
}

func (a *app) episodeTile(c *ui.Context, i int) {
	cur := i == a.current
	ep := ui.ButtonBase(c.Key(i)).Size(62, 44).Radius(6).Center().Label(fmt.Sprintf("第 %d 集", i+1))
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
	ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Radius(10).Clip().Background(rgba(0, 0, 0, 255)).Children(func() {
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
