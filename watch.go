package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

const epBlock = 50 // episodes per range of the list

func (a *app) watchPage(c *ui.Context) {
	s := a.series
	if s == nil {
		return
	}
	if a.detail != nil {
		s = &a.detail.Series
	}
	w, h := c.Size()
	cv := a.coverOf(s.Cover)
	playerH := max(h-navHeight-40, 360)
	playerW := min(playerH*9/16, w*0.42)
	playerH = playerW * 16 / 9
	const backdropH = 520
	ui.Column(c).Children(func() {
		// The poster, blurred, behind the top of the page. Elements placed
		// with Absolute paint over their siblings in the flow, so the
		// backdrop keeps them in a box of its own and the content follows
		// it, pulled up over it.
		ui.Box(c).Height(backdropH).Clip().Children(func() {
			if cv != nil && cv.img != nil {
				ui.Image(c, cv.img).Absolute().Left(-60).Top(-60).Size(w+120, backdropH+80).Fit(ui.Cover).Blur(48).Opacity(0.45)
			}
			ui.Box(c).Absolute().Left(0).Top(0).Size(w, backdropH).LinearGradient(ui.LinearGradient{
				Angle: 180, From: withAlpha(colorBG, 60), To: colorBG})
		})
		ui.Row(c).Margin(-backdropH, 0, 0, 0).Padding(navHeight+12, pagePad, 40, pagePad).Gap(36).AlignItems(ui.Start).Children(func() {
			ui.WebView(c, a.player).Width(playerW).Height(playerH).Shrink(0).Radius(12).
				Background(rgba(0, 0, 0, 255)).Shadow(0, 20, 60, 0, rgba(0, 0, 0, 160)).
				Children(func() { a.playerOverlay(c) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(16).Padding(8, 0, 0, 0).Children(func() { a.watchInfo(c) })
		})
	})
}

func (a *app) watchInfo(c *ui.Context) {
	s := *a.series
	if a.detail != nil {
		s = a.detail.Series
	}
	ui.Text(c, s.Title).FontSize(34).FontWeight(800).LineHeight(1.2).Selectable()
	metaRow(c, s, 13)
	if s.Intro != "" {
		ui.Text(c, s.Intro).FontSize(14).TextColor(rgba(215, 215, 215, 255)).LineHeight(1.6).MaxWidth(760).Selectable()
	}
	if a.detail == nil {
		if a.detailErr != "" {
			ui.Text(c, a.detailErr).TextColor(colorRed)
		}
		return
	}
	n := len(a.detail.VIDs)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Margin(4, 0, 0, 0).Children(func() {
		label := fmt.Sprintf("正在播放 第 %d 集", a.current+1)
		if bigButton(c, "replay", iconPlayFill, label, true).Clicked() {
			a.play(a.current)
		}
		if a.current+1 < n && bigButton(c, "next", iconNext, "下一集", false).Clicked() {
			a.play(a.current + 1)
		}
		ui.Spacer(c)
		ui.Text(c, "自动连播").FontSize(13).TextColor(colorMuted)
		ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
	})
	ui.Box(c).Height(1).Background(rgba(255, 255, 255, 30)).Margin(10, 0, 4, 0)
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, "选集").FontSize(20).FontWeight(700)
		ui.Textf(c, "共 %d 集", n).FontSize(13).TextColor(colorFaint)
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
		if cur && a.playing {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				equalizer(c)
				ui.Textf(c, "%d", i+1).FontSize(14).FontWeight(700)
			})
		} else {
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

// playerOverlay covers the web view while the episode is prepared.
func (a *app) playerOverlay(c *ui.Context) {
	if a.playing && a.job != nil {
		return
	}
	white := rgba(255, 255, 255, 235)
	dim := rgba(255, 255, 255, 150)
	ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Radius(12).Clip().Background(rgba(0, 0, 0, 255)).Children(func() {
		if a.series != nil {
			if cv := a.coverOf(a.series.Cover); cv != nil && cv.img != nil {
				ui.Image(c, cv.img).Fill().Fit(ui.Cover).Blur(24).Opacity(0.35)
			}
		}
		ui.Column(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Center().Gap(12).Padding(28).Children(func() {
			switch {
			case a.detailErr != "":
				ui.Text(c, a.detailErr).TextColor(white).TextAlign(ui.Center)
			case a.detail == nil || a.job == nil:
				ui.Spinner(c).TextColor(white).FontSize(26)
				ui.Text(c, "正在加载剧集…").TextColor(dim).FontSize(13)
			case a.job.Finished() && a.job.err != nil:
				ui.Text(c, a.job.err.Error()).TextColor(white).TextAlign(ui.Center).FontSize(13)
				if bigButton(c, "retry-play", iconRefresh, "重试", true).Clicked() {
					a.play(a.current)
				}
			default:
				j := a.job
				c.After(150 * time.Millisecond) // follow the download
				ui.Spinner(c).TextColor(white).FontSize(26)
				ui.Textf(c, "第 %d 集", a.current+1).TextColor(white).FontSize(18).FontWeight(700)
				received, total := j.received.Load(), j.total.Load()
				switch j.Phase() {
				case "resolving":
					ui.Text(c, "正在获取片源…").TextColor(dim).FontSize(13)
				case "downloading":
					ui.Text(c, "正在缓冲 "+j.Quality()).TextColor(dim).FontSize(13)
					if total > 0 {
						ui.Box(c).Width(180).Height(4).Radius(2).Background(rgba(255, 255, 255, 50)).Children(func() {
							ui.Box(c).Width(180 * float32(received) / float32(total)).Height(4).Radius(2).Background(colorRed)
						})
						ui.Textf(c, "%.1f / %.1f MB", mb(received), mb(total)).TextColor(dim).FontSize(11)
					}
				case "decrypting":
					ui.Text(c, "正在解密…").TextColor(dim).FontSize(13)
				default:
					ui.Text(c, "即将播放").TextColor(dim).FontSize(13)
				}
			}
		})
	})
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }
