package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

func (a *app) view(c *ui.Context) {
	if a.player == nil {
		return // the window can draw before its web view is made
	}
	t := c.Theme()
	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(sidebarWidth).Shrink(0)
		if !vibrant {
			side.Background(t.Surface)
		}
		side.Children(func() { a.sidebar(c, vibrant) })
		ui.Box(c).Width(1).Background(t.Border)
		ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
			if a.section == "watch" {
				a.watchPane(c)
			} else {
				a.listPane(c)
			}
		})
	})
}

func (a *app) sidebar(c *ui.Context, vibrant bool) {
	ui.Box(c).Height(titleBarHeight).Shrink(0).DragWindow()
	ui.Box(c).Padding(0, 10, 8, 10).Children(func() {
		if ui.SearchField(c.Key("search"), &a.query).Placeholder("搜索短剧").Label("搜索短剧").Submitted() &&
			strings.TrimSpace(a.query) != "" {
			a.searched = strings.TrimSpace(a.query)
			a.section = "search"
			a.loadList(1)
		}
	})
	chosen := a.section
	list := ui.Sidebar(c, &chosen, func() {
		ui.SidebarSection(c, "发现", nil, func() {
			icons := []*ui.SVG{iconFilm, iconPalette, iconSparkles}
			for i, cat := range hongguo.Categories {
				ui.SidebarItem(c, cat.Slug, icons[i%len(icons)], cat.Label)
			}
			if a.searched != "" {
				ui.SidebarItem(c, "search", iconSearch, "搜索：“"+a.searched+"”")
			}
		})
		if a.series != nil {
			ui.SidebarSection(c, "正在播放", nil, func() {
				ui.SidebarItem(c, "watch", iconPlay, a.series.Title)
			})
		}
	}).Grow(1).Label("分类")
	if vibrant {
		list.Background(ui.Transparent)
	}
	if list.Changed() && chosen != a.section {
		a.section = chosen
		if chosen != "watch" {
			a.lastList = chosen
			a.loadList(1)
		}
	}
}

// header is a pane's title bar, which drags the window.
func header(c *ui.Context, build func()) {
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Gap(8).
		Padding(0, 16, 0, 20).DragWindow().Children(build)
	ui.Divider(c)
}

func (a *app) listTitle() string {
	if a.section == "search" {
		return "搜索结果"
	}
	for _, cat := range hongguo.Categories {
		if cat.Slug == a.section {
			return cat.Label
		}
	}
	return ""
}

func (a *app) listPane(c *ui.Context) {
	t := c.Theme()
	header(c, func() {
		ui.Text(c, a.listTitle()).FontSize(15).Bold().SingleLine()
		if a.list.totalPages > 0 {
			ui.Textf(c, "第 %d / %d 页", a.list.page, a.list.totalPages).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if a.list.loading {
			ui.Spinner(c)
		}
		if ui.Button(c.Key("prev"), "上一页").Disabled(a.list.loading || a.list.page <= 1).Clicked() {
			a.loadList(a.list.page - 1)
		}
		if ui.Button(c.Key("next"), "下一页").Disabled(a.list.loading || a.list.page >= a.list.totalPages).Clicked() {
			a.loadList(a.list.page + 1)
		}
	})
	switch {
	case a.list.err != "":
		a.message(c, a.list.err, func() { a.loadList(max(a.list.page, 1)) })
		return
	case len(a.list.items) == 0 && a.list.loading:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).FontSize(22) })
		return
	case len(a.list.items) == 0:
		a.message(c, "没有找到相关短剧", nil)
		return
	}
	items := a.list.items
	ui.GridView(c, &a.grid, len(items), 156, 300, func(i int) {
		s := items[i]
		card := ui.Column(c.Key(s.ID)).Grow(1).Gap(6).Padding(6).Radius(12)
		if card.Hovered() {
			card.Background(t.SurfaceHover)
		}
		card.Label(s.Title).Tooltip(s.Intro)
		if card.Clicked() {
			a.open(s)
		}
		card.Children(func() {
			poster(c, a.coverOf(s.Cover), t).Grow(1)
			ui.Text(c, s.Title).FontSize(13).FontWeight(600).MaxLines(2).Lines(2, 2)
			ui.Text(c, strings.Join(append([]string{s.EpisodeLabel}, firstN(s.Tags, 2)...), " · ")).
				FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
	}).Grow(1).Gap(8).Padding(12, 14)
}

func poster(c *ui.Context, cv *cover, t *ui.Theme) ui.Element {
	if cv != nil && cv.img != nil {
		return ui.Image(c, cv.img).Fit(ui.Cover).Radius(9).MinHeight(0)
	}
	return ui.Box(c).Radius(9).Background(t.Surface).Border(1, t.Border).MinHeight(0)
}

func (a *app) message(c *ui.Context, msg string, retry func()) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		ui.Text(c, msg).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(420)
		if retry != nil && ui.Button(c.Key("retry"), "重试").Clicked() {
			retry()
		}
	})
}

func (a *app) watchPane(c *ui.Context) {
	t := c.Theme()
	header(c, func() {
		back := ui.ButtonBase(c.Key("back")).Size(28, 28).Radius(6).Center().Label("返回").Tooltip("返回")
		if back.Hovered() {
			back.Background(t.SurfaceHover)
		}
		back.Children(func() { ui.Icon(c, iconBack).Size(16, 16) })
		if back.Clicked() {
			a.section = a.lastList
		}
		title := ""
		if a.series != nil {
			title = a.series.Title
		}
		ui.Text(c, title).FontSize(15).Bold().SingleLine().Shrink(1).MinWidth(0)
		ui.Spacer(c)
	})
	_, h := c.Size()
	playerH := max(h-titleBarHeight-32, 200)
	playerW := playerH * 9 / 16
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(16).Gap(20).Children(func() {
		ui.WebView(c, a.player).Width(playerW).Shrink(0).Radius(12).Background(ui.RGBA(0, 0, 0, 255)).
			Children(func() { a.playerOverlay(c) })
		ui.Scroll(c).Grow(1).MinWidth(0).Children(func() { a.infoColumn(c) })
	})
}

func (a *app) playerOverlay(c *ui.Context) {
	white := ui.RGBA(255, 255, 255, 230)
	dim := ui.RGBA(255, 255, 255, 150)
	overlay := func(build func()) {
		ui.Column(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Center().Gap(10).Padding(24).
			Background(ui.RGBA(0, 0, 0, 255)).Children(build)
	}
	switch {
	case a.detailErr != "":
		overlay(func() { ui.Text(c, a.detailErr).TextColor(white).TextAlign(ui.Center) })
	case a.detail == nil:
		overlay(func() { ui.Spinner(c).TextColor(white).FontSize(22) })
	case a.job != nil && !a.playing:
		j := a.job
		if j.Finished() && j.err != nil {
			overlay(func() {
				ui.Text(c, j.err.Error()).TextColor(white).TextAlign(ui.Center).FontSize(13)
				if ui.Button(c.Key("retry-play"), "重试").Clicked() {
					a.play(a.current)
				}
			})
			return
		}
		c.After(150 * time.Millisecond) // follow the download
		overlay(func() {
			ui.Spinner(c).TextColor(white).FontSize(22)
			received, total := j.received.Load(), j.total.Load()
			switch j.Phase() {
			case "resolving":
				ui.Text(c, "正在获取片源…").TextColor(white).FontSize(13)
			case "downloading":
				ui.Text(c, "正在缓冲 "+j.Quality()).TextColor(white).FontSize(13)
				if total > 0 {
					ui.Progress(c, float64(received)/float64(total)).Width(160)
					ui.Textf(c, "%.1f / %.1f MB", mb(received), mb(total)).TextColor(dim).FontSize(11)
				}
			case "decrypting":
				ui.Text(c, "正在解密…").TextColor(white).FontSize(13)
			default:
				ui.Text(c, "即将播放").TextColor(white).FontSize(13)
			}
		})
	}
}

func (a *app) infoColumn(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(14).Padding(0, 8, 16, 0).MaxWidth(720).Children(func() {
		s := a.series
		if a.detail != nil {
			s = &a.detail.Series
		}
		if s == nil {
			return
		}
		ui.Text(c, s.Title).FontSize(22).Bold()
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if s.EpisodeLabel != "" {
				ui.Badge(c, s.EpisodeLabel)
			}
			for _, tag := range s.Tags {
				ui.Text(c, tag).FontSize(11).TextColor(t.TextMuted).Padding(2, 8).Radius(10).Background(t.Surface)
			}
		})
		if s.Intro != "" {
			ui.Text(c, s.Intro).FontSize(13).TextColor(t.TextMuted).LineHeight(1.5).Selectable()
		}
		if a.detail == nil {
			return
		}
		n := len(a.detail.VIDs)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Margin(6, 0, 0, 0).Children(func() {
			ui.Text(c, "选集").FontSize(15).Bold()
			if a.current >= 0 {
				ui.Textf(c, "正在播放第 %d 集 · 共 %d 集", a.current+1, n).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			ui.Text(c, "自动连播").FontSize(12).TextColor(t.TextMuted)
			ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
			if ui.Button(c.Key("prev-ep"), "").Disabled(a.current <= 0).Tooltip("上一集").Label("上一集").Children(func() {
				ui.Icon(c, iconPrev).Size(13, 13)
			}).Clicked() {
				a.play(a.current - 1)
			}
			if ui.Button(c.Key("next-ep"), "").Disabled(a.current >= n-1).Tooltip("下一集").Label("下一集").Children(func() {
				ui.Icon(c, iconNext).Size(13, 13)
			}).Clicked() {
				a.play(a.current + 1)
			}
		})
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for i := range n {
				ep := ui.ButtonBase(c.Key(i)).Size(54, 36).Radius(8).Center().Label(fmt.Sprintf("第 %d 集", i+1))
				switch {
				case i == a.current:
					ep.Background(t.Accent).TextColor(t.AccentText)
				case ep.Hovered():
					ep.Background(t.SurfaceHover)
				default:
					ep.Background(t.Surface)
				}
				ep.Children(func() { ui.Textf(c, "%d", i+1).FontSize(13).FontWeight(500) })
				if ep.Clicked() && i != a.current {
					a.play(i)
				}
			}
		})
	})
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
