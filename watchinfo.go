package main

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

var (
	colorPill = rgba(255, 255, 255, 26)
	colorLink = rgba(62, 166, 255, 255)
	colorStar = rgba(255, 196, 0, 255)
)

// currentVID is the episode that plays, if any.
func (a *app) currentVID() string {
	if a.detail == nil || a.current < 0 || a.current >= len(a.detail.VIDs) {
		return ""
	}
	return a.detail.VIDs[a.current]
}

// watchHeader is the episode's title, and below it the series, as video
// sites show a channel, with its actions on the right.
func (a *app) watchHeader(c *ui.Context, s hongguo.Series) {
	title := s.Title
	if a.current >= 0 {
		title += fmt.Sprintf("  第 %d 集", a.current+1)
	}
	ui.Text(c, title).FontSize(20).FontWeight(700).LineHeight(1.3).MaxLines(2).Selectable()
	info := a.seriesInfo(s.ID).value
	var ep *hongguo.EpisodeInfo
	if vid := a.currentVID(); vid != "" {
		ep = a.episodeInfo(s.ID, vid).value
	}
	ui.Row(c).Wrap().Justify(ui.SpaceBetween).AlignItems(ui.Center).GapX(16).GapY(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			avatar(c, a, s.Cover, s.Title, 40)
			ui.Column(c).Gap(2).MaxWidth(280).Children(func() {
				ui.Text(c, s.Title).FontSize(15).FontWeight(700).SingleLine()
				var sub []string
				if s.EpisodeLabel != "" {
					sub = append(sub, s.EpisodeLabel)
				}
				if info != nil && info.Heat != "" {
					sub = append(sub, info.Heat)
				}
				ui.Text(c, strings.Join(sub, " · ")).FontSize(12).TextColor(colorMuted).SingleLine()
			})
			a.listPill(c, s)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if ep != nil && ep.Likes > 0 {
				statPill(c, "likes", iconThumb, count(ep.Likes), "本集点赞")
			}
			if info != nil && info.Favorites > 0 {
				statPill(c, "favs", iconStar, count(info.Favorites), "全剧收藏")
			}
			n := 0
			if a.detail != nil {
				n = len(a.detail.VIDs)
			}
			if a.current > 0 && pill(c, "prev", iconPrev, "上一集").Clicked() {
				a.play(a.current - 1)
			}
			if a.current >= 0 && a.current+1 < n && pill(c, "next", iconNext, "下一集").Clicked() {
				a.play(a.current + 1)
			}
		})
	})
}

// pill is a round-ended button with an icon and a label.
func pill(c *ui.Context, key string, ico *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(36).Padding(0, 16, 0, 12).Radius(18).Gap(6).AlignItems(ui.Center).Label(label)
	b.Background(rgba(255, 255, 255, 26+20*b2f(b.Hovered())))
	b.Children(func() {
		ui.Icon(c, ico).Size(18, 18).TextColor(colorWhite)
		ui.Text(c, label).FontSize(14).FontWeight(600).SingleLine()
	})
	return b
}

// statPill shows a number the site counts, as a pill that does nothing.
func statPill(c *ui.Context, key string, ico *ui.SVG, value, tip string) {
	ui.Row(c.Key(key)).Height(36).Padding(0, 16, 0, 12).Radius(18).Gap(6).AlignItems(ui.Center).
		Background(colorPill).Tooltip(tip).Label(tip + " " + value).Children(func() {
		ui.Icon(c, ico).Size(18, 18).TextColor(colorWhite)
		ui.Text(c, value).FontSize(14).FontWeight(600).SingleLine()
	})
}

// listPill adds the series to My List, white until it is in it, as video
// sites have their subscribe button.
func (a *app) listPill(c *ui.Context, s hongguo.Series) {
	in := a.inMyList(s.ID)
	label := map[bool]string{true: "已在片单", false: "加入片单"}[in]
	b := ui.ButtonBase(c.Key("list-pill")).Height(36).Padding(0, 16, 0, 12).Radius(18).Gap(6).AlignItems(ui.Center).
		Label(map[bool]string{true: "从我的片单移除", false: "加入我的片单"}[in])
	fg, hover := rgba(15, 15, 15, 255), b.Hovered()
	switch {
	case in:
		fg = colorWhite
		b.Background(rgba(255, 255, 255, 26+20*b2f(hover)))
	case hover:
		b.Background(rgba(217, 217, 217, 255))
	default:
		b.Background(colorWhite)
	}
	ico := iconPlus
	if in {
		ico = iconCheck
	}
	b.Children(func() {
		ui.Icon(c, ico).Size(18, 18).TextColor(fg)
		ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		a.toggleMyList(c, s)
	}
}

// avatar is a round picture, or the first letter of name on a color of
// its own while there is none.
func avatar(c *ui.Context, a *app, url, name string, size float32) {
	box := ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Clip()
	pic := a.images.Get(c, url, imgSmall, box.InView())
	box.Children(func() {
		if pic.Img == nil || pic.Fade < 1 {
			h := fnv.New32a()
			h.Write([]byte(name))
			initial := "?"
			if r := []rune(strings.TrimSpace(name)); len(r) > 0 {
				initial = string(r[0])
			}
			ui.Box(c).Fill().Center().Background(hsl(float32(h.Sum32()%360), 0.45, 0.42)).Children(func() {
				ui.Text(c, initial).FontSize(size * 0.42).FontWeight(600).TextColor(colorWhite)
			})
		}
		if pic.Img != nil {
			ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
		}
	})
}

// description is the series' standing, tags and synopsis in a box that
// opens to the whole of them and the cast.
func (a *app) description(c *ui.Context, s hongguo.Series) {
	info := a.seriesInfo(s.ID).value
	open := a.descOpen
	box := ui.Column(c.Key("desc")).Padding(12, 14).Radius(12).Gap(8)
	if !open {
		box.Cursor(ui.CursorPointer)
		if box.Clicked() {
			a.descOpen = true
		}
	}
	box.Background(rgba(255, 255, 255, 26+12*b2f(!open && box.Hovered())))
	box.Children(func() {
		var stats []string
		if info != nil {
			if info.Rating > 0 {
				stats = append(stats, fmt.Sprintf("★ %.1f 分", info.Rating))
			}
			if info.RatingCount > 0 {
				stats = append(stats, count(info.RatingCount)+" 人评分")
			}
			if info.Likes > 0 {
				stats = append(stats, count(info.Likes)+" 点赞")
			}
			if !info.FirstShown.IsZero() {
				stats = append(stats, "首播 "+info.FirstShown.Format("2006-01-02"))
			}
		}
		if s.EpisodeLabel != "" {
			stats = append(stats, s.EpisodeLabel)
		}
		if len(stats) > 0 {
			ui.Text(c, strings.Join(stats, "   ")).FontSize(14).FontWeight(700)
		}
		tags := s.Tags
		if info != nil && len(info.Tags) > 0 {
			tags = info.Tags
		}
		if len(tags) > 0 {
			ui.Text(c, "#"+strings.Join(tags, "  #")).FontSize(14).TextColor(colorLink).MaxLines(map[bool]int{true: 0, false: 1}[open])
		}
		intro := ui.Text(c, s.Intro).FontSize(14).LineHeight(1.6).TextColor(rgba(230, 230, 230, 255))
		if !open {
			intro.MaxLines(2)
			ui.Text(c, "…更多").FontSize(14).FontWeight(700)
			return
		}
		intro.Selectable()
		if info != nil && len(info.Cast) > 0 {
			ui.Text(c, "演员").FontSize(15).FontWeight(700).Margin(8, 0, 0, 0)
			ui.Row(c).Wrap().GapX(20).GapY(14).Children(func() {
				for i, p := range info.Cast {
					ui.Column(c.Key(fmt.Sprint("cast", i))).Width(72).Gap(4).AlignItems(ui.Center).Children(func() {
						avatar(c, a, p.Avatar, p.Name, 56)
						ui.Text(c, p.Name).FontSize(12).FontWeight(600).SingleLine()
						ui.Text(c, p.Role).FontSize(11).TextColor(colorFaint).SingleLine()
					})
				}
			})
		}
		ui.Text(c, "快捷键：空格 播放/暂停 · ←/→ 快退/快进 5 秒 · 长按 → 2 倍速 · ↑/↓ 音量 · M 静音 · [ / ] 倍速 · F 全屏 · Shift+←/→ 上/下一集").
			FontSize(12).TextColor(colorFaint).Margin(6, 0, 0, 0)
		ui.Row(c).Children(func() {
			less := ui.ButtonBase(c.Key("desc-less")).Label("收起")
			less.Children(func() { ui.Text(c, "收起").FontSize(14).FontWeight(700) })
			if less.Clicked() {
				a.descOpen = false
			}
		})
	})
}

// episodePanel lists the episodes, as video sites list a playlist beside
// the video.
func (a *app) episodePanel(c *ui.Context, s hongguo.Series, width float32) {
	ui.Column(c.Key("episodes")).Width(width).Radius(12).Clip().Border(1, rgba(255, 255, 255, 34)).Children(func() {
		ui.Row(c).Padding(12, 16).Gap(12).AlignItems(ui.Center).Background(rgba(255, 255, 255, 16)).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "选集").FontSize(16).FontWeight(700)
				if a.detail != nil && a.current >= 0 {
					ui.Textf(c, "正在播放第 %d 集 · 共 %d 集", a.current+1, len(a.detail.VIDs)).FontSize(12).TextColor(colorMuted)
				}
			})
			ui.Text(c, "自动连播").FontSize(12).TextColor(colorMuted)
			ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
		})
		ui.Column(c).Padding(12).Gap(10).Children(func() {
			if a.detail == nil {
				if a.detailErr != "" {
					ui.Text(c, a.detailErr).FontSize(13).TextColor(colorRed)
				} else {
					ui.Spinner(c).FontSize(18)
				}
				return
			}
			n := len(a.detail.VIDs)
			if blocks := (n + epBlock - 1) / epBlock; blocks > 1 {
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for b := range blocks {
						lo, hi := b*epBlock+1, min((b+1)*epBlock, n)
						p := ui.ButtonBase(c.Key(fmt.Sprint("blk", b))).Padding(4, 12).Radius(8).Label(fmt.Sprintf("%d-%d", lo, hi))
						active := b == a.epRange
						switch {
						case active:
							p.Background(colorWhite)
						case p.Hovered():
							p.Background(rgba(255, 255, 255, 40))
						default:
							p.Background(rgba(255, 255, 255, 18))
						}
						p.Children(func() {
							ui.Textf(c, "%d-%d", lo, hi).FontSize(12).FontWeight(600).
								TextColor(map[bool]ui.Color{true: rgba(0, 0, 0, 255), false: rgba(220, 220, 220, 255)}[active])
						})
						if p.Clicked() {
							a.epRange = b
						}
					}
				})
			}
			const gap, tileH = 6, 40
			inner := width - 24 - 2 - 10 // the padding, the border and room for the scroll bar
			cols := max(1, int((inner+gap)/(52+gap)))
			tw := (inner-gap*float32(cols-1))/float32(cols) - 0.01
			lo, hi := a.epRange*epBlock, min((a.epRange+1)*epBlock, n)
			rows := (hi - lo + cols - 1) / cols
			gridH := float32(rows)*(tileH+gap) - gap
			// Some six rows show; the rest scroll inside the panel.
			ui.Scroll(c.Key(fmt.Sprint("ep-scroll", a.epRange))).Height(min(gridH, 6*(tileH+gap)-gap+tileH/2)).Children(func() {
				ui.Row(c).Wrap().Gap(gap).Children(func() {
					for i := lo; i < hi; i++ {
						a.episodeTile(c, i, tw)
					}
				})
			})
		})
	})
}

// comments shows the comments on the episode, or on the series, that the
// site shows: a few of them, newest liked first.
func (a *app) comments(c *ui.Context, s hongguo.Series) {
	info := a.seriesInfo(s.ID)
	vid := a.currentVID()
	var ep *loaded[hongguo.EpisodeInfo]
	if vid != "" {
		ep = a.episodeInfo(s.ID, vid)
	}
	ui.Column(c.Key("comments")).Gap(18).Margin(10, 0, 0, 0).Children(func() {
		var total int
		switch {
		case a.seriesTab && info.value != nil:
			total = info.value.Reviews.Total
		case !a.seriesTab && ep != nil && ep.value != nil:
			total = ep.value.Reviews.Total
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
			head := "评论"
			if total > 0 {
				head = count(total) + " 条评论"
			}
			ui.Text(c, head).FontSize(20).FontWeight(700)
			for _, t := range []struct {
				series bool
				label  string
			}{{false, "本集评论"}, {true, "全剧剧评"}} {
				on := a.seriesTab == t.series
				chip := ui.ButtonBase(c.Key(t.label)).Height(32).Padding(0, 12).Radius(8).Center().Label(t.label)
				switch {
				case on:
					chip.Background(colorWhite)
				case chip.Hovered():
					chip.Background(rgba(255, 255, 255, 46))
				default:
					chip.Background(colorPill)
				}
				chip.Children(func() {
					ui.Text(c, t.label).FontSize(13).FontWeight(600).
						TextColor(map[bool]ui.Color{true: rgba(15, 15, 15, 255), false: colorWhite}[on])
				})
				if chip.Clicked() {
					a.seriesTab = t.series
				}
			}
		})
		var list []hongguo.Review
		retry := func() {}
		var err error
		loading := false
		if a.seriesTab {
			loading, err = info.loading, info.err
			retry = func() { a.loadInfo(s.ID, info) }
			if info.value != nil {
				list = info.value.Reviews.Items
			}
		} else if ep == nil {
			loading = true
		} else {
			loading, err = ep.loading, ep.err
			retry = func() { a.loadEpisodeInfo(s.ID, vid, ep) }
			if ep.value != nil {
				list = ep.value.Reviews.Items
			}
		}
		switch {
		case loading:
			ui.Spinner(c).FontSize(20)
			return
		case isLocked(err):
			ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, "官网未开放这一集的评论。").FontSize(14).TextColor(colorMuted)
				if linkButton(c, "to-series", "看全剧剧评").Clicked() {
					a.seriesTab = true
				}
			})
			return
		case err != nil:
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "评论加载失败："+err.Error()).FontSize(14).TextColor(colorMuted)
				if linkButton(c, "retry-comments", "重试").Clicked() {
					retry()
				}
			})
			return
		case len(list) == 0:
			ui.Text(c, "还没有评论。").FontSize(14).TextColor(colorMuted)
			return
		}
		now := time.Now()
		for i, r := range list {
			comment(c, a, fmt.Sprint("c", i), r, now)
		}
		if total > len(list) {
			ui.Textf(c, "官网只公开了 %d 条评论，更多请到红果短剧 App 查看。", len(list)).FontSize(12).TextColor(colorFaint)
		}
	})
}

func comment(c *ui.Context, a *app, key string, r hongguo.Review, now time.Time) {
	ui.Row(c.Key(key)).Gap(14).AlignItems(ui.Start).Children(func() {
		avatar(c, a, r.Avatar, r.Author, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				name := r.Author
				if name == "" {
					name = "匿名用户"
				}
				ui.Text(c, "@"+name).FontSize(13).FontWeight(600).SingleLine()
				if !r.Time.IsZero() {
					ui.Text(c, ago(r.Time, now)).FontSize(12).TextColor(colorFaint).SingleLine()
				}
				if r.Rating > 0 {
					ui.Textf(c, "★ %g", r.Rating).FontSize(12).FontWeight(600).TextColor(colorStar)
				}
			})
			ui.Text(c, r.Content).FontSize(14).LineHeight(1.55).Selectable()
			ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(2, 0, 0, 0).Children(func() {
				ui.Icon(c, iconThumb).Size(16, 16).TextColor(colorMuted)
				if r.Likes > 0 {
					ui.Text(c, count(r.Likes)).FontSize(12).TextColor(colorMuted)
				}
			})
		})
	})
}

// recommended lists series to watch next: those sharing the most tags
// with s, then the catalog's.
func (a *app) recommended(c *ui.Context, s hongguo.Series, width float32) {
	list := a.recommend(s, 20)
	if len(list) == 0 {
		return
	}
	ui.Column(c.Key("recommended")).Gap(8).Children(func() {
		ui.Text(c, "相关推荐").FontSize(16).FontWeight(700).Margin(0, 0, 4, 0)
		for _, o := range list {
			a.recCard(c, o, width)
		}
	})
}

func (a *app) recommend(s hongguo.Series, n int) []hongguo.Series {
	out := a.similar(s, n)
	seen := map[string]bool{s.ID: true}
	for _, o := range out {
		seen[o.ID] = true
	}
	for _, cat := range hongguo.Categories {
		for _, o := range a.feed(cat.Slug).items {
			if len(out) >= n {
				return out
			}
			if !seen[o.ID] {
				seen[o.ID] = true
				out = append(out, o)
			}
		}
	}
	return out
}

// recCard is a series in the side column: its poster, with its title,
// tags and episodes beside it.
func (a *app) recCard(c *ui.Context, o hongguo.Series, width float32) {
	card := ui.ButtonBase(c.Key("rec" + o.ID)).Width(width).Padding(4).Radius(10).Gap(12).AlignItems(ui.Start).Label(o.Title)
	hover := card.Hovered()
	if hover {
		card.Background(rgba(255, 255, 255, 18))
	}
	card.Children(func() {
		ui.Box(c).Size(84, 112).Shrink(0).Radius(8).Clip().Background(colorCard).Children(func() {
			pic := a.images.Get(c, o.Cover, imgSmall, card.InView())
			if pic.Img == nil || pic.Fade < 1 {
				posterFallback(c, o.Title, pic.Failed)
			}
			if pic.Img != nil {
				ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Padding(2, 0, 0, 0).Children(func() {
			ui.Text(c, o.Title).FontSize(14).FontWeight(600).LineHeight(1.35).MaxLines(2)
			if len(o.Tags) > 0 {
				ui.Text(c, strings.Join(firstN(o.Tags, 3), " · ")).FontSize(12).TextColor(colorMuted).SingleLine()
			}
			sub := o.EpisodeLabel
			if w, ok := a.watchOf(o.ID); ok {
				sub = watchNote(w)
			}
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(colorFaint).SingleLine()
			}
		})
	})
	if card.Clicked() {
		a.open(o)
	}
}
