package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

const (
	pagePad   = 48 // the side margins of pages
	cardW     = 168
	cardGap   = 10
	heroCount = 6 // how many series the home banner cycles through
	heroCycle = 9 * time.Second
)

func (a *app) heroHeight(c *ui.Context) float32 {
	_, h := c.Size()
	return min(max(h*0.66, 420), 640)
}

// --- home ---

func (a *app) homePage(c *ui.Context) {
	featured := a.feed(hongguo.Categories[0].Slug)
	if n := min(len(featured.items), heroCount); n > 0 {
		if time.Since(a.heroAt) >= heroCycle {
			a.hero = (a.hero + 1) % n
			a.heroAt = time.Now()
		}
		c.After(heroCycle - time.Since(a.heroAt))
		a.heroBanner(c, featured.items[a.hero%n], func() {
			a.heroDots(c, n)
		})
	} else {
		a.heroPlaceholder(c, featured)
	}
	ui.Column(c).Gap(30).Padding(0, 0, 56, 0).Margin(-70, 0, 0, 0).Children(func() {
		if len(a.history) > 0 {
			a.continueRow(c)
		}
		titles := map[string]string{"real-drama": "热播真人剧", "comic-drama": "人气漫剧", "ai-drama": "AI 剧场"}
		for _, cat := range hongguo.Categories {
			f := a.feed(cat.Slug)
			a.posterRow(c, cat.Slug, titles[cat.Slug], f, func() { a.go_(cat.Slug); a.scroll(cat.Slug).Y = 0 })
		}
	})
}

func (a *app) heroDots(c *ui.Context, n int) {
	ui.Row(c).Gap(6).Margin(14, 0, 0, 0).Children(func() {
		for i := range n {
			dot := ui.ButtonBase(c.Key(fmt.Sprint("dot", i))).Height(4).Radius(2).Label(fmt.Sprintf("第 %d 部", i+1))
			w := dot.Animate("w", map[bool]float32{true: 26, false: 10}[i == a.hero], 250*time.Millisecond)
			dot.Width(w).Background(withAlpha(colorWhite, map[bool]uint8{true: 230, false: 90}[i == a.hero]))
			if dot.Clicked() {
				a.hero, a.heroAt = i, time.Now()
			}
		}
	})
}

func (a *app) heroPlaceholder(c *ui.Context, f *feed) {
	ui.Column(c).Height(a.heroHeight(c)).Center().Gap(12).Children(func() {
		if f.err != "" {
			ui.Text(c, f.err).TextColor(colorMuted)
			if ui.Button(c.Key("retry-hero"), "重试").Clicked() {
				a.loadFeed(hongguo.Categories[0].Slug, 1)
			}
		} else {
			ui.Spinner(c).FontSize(26).TextColor(colorMuted)
		}
	})
}

// heroBanner is the big featured series at the top of a page: its poster
// blurred across the width, the poster itself at the right, and its title,
// synopsis and actions at the left.
func (a *app) heroBanner(c *ui.Context, s hongguo.Series, extra func()) {
	w, _ := c.Size()
	h := a.heroHeight(c)
	cv := a.coverOf(s.Cover)
	ui.Box(c.Key("hero-" + s.ID)).Height(h).Clip().Children(func() {
		if cv != nil && cv.img != nil {
			ui.Image(c, cv.img).Absolute().Left(-60).Top(-60).Size(w+120, h+120).Fit(ui.Cover).Blur(36).Opacity(0.75)
		}
		// Darken toward the text, and fade into the page below.
		ui.Box(c).Absolute().Left(0).Top(0).Size(w, h).LinearGradient(ui.LinearGradient{
			Angle: 90, Stops: []ui.GradientStop{{Color: withAlpha(colorBG, 250), At: 0}, {Color: withAlpha(colorBG, 170), At: 0.45}, {Color: withAlpha(colorBG, 40), At: 1}}})
		ui.Box(c).Absolute().Left(0).Bottom(0).Size(w, h*0.45).LinearGradient(ui.LinearGradient{
			Angle: 180, From: withAlpha(colorBG, 0), To: colorBG})
		ui.Row(c).Absolute().Left(pagePad).Right(pagePad).Top(navHeight + 28).Bottom(96).AlignItems(ui.Center).Gap(40).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).MaxWidth(620).Gap(14).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "红果").FontSize(13).FontWeight(900).TextColor(colorRed)
					ui.Text(c, "独家短剧").FontSize(12).FontWeight(600).TextColor(colorMuted).LetterSpacing(3)
				})
				ui.Text(c, s.Title).FontSize(42).FontWeight(800).MaxLines(2).LineHeight(1.15)
				metaRow(c, s, 13)
				if s.Intro != "" {
					ui.Text(c, s.Intro).FontSize(15).TextColor(rgba(230, 230, 230, 255)).MaxLines(3).LineHeight(1.5)
				}
				ui.Row(c).Gap(12).Margin(8, 0, 0, 0).Children(func() {
					label := "播放"
					if ep, ok := a.lastEpisode(s.ID); ok {
						label = fmt.Sprintf("继续第 %d 集", ep+1)
					}
					if bigButton(c, "play-"+s.ID, iconPlayFill, label, true).Clicked() {
						a.open(s)
					}
					if bigButton(c, "info-"+s.ID, iconInfo, "详情", false).Clicked() {
						a.open(s)
					}
				})
				if extra != nil {
					extra()
				}
			})
			if cv != nil && cv.img != nil {
				ph := h - navHeight - 28 - 96
				ui.Image(c, cv.img).Height(ph).Width(ph*3/4).Shrink(0).Fit(ui.Cover).Radius(10).
					Shadow(0, 18, 50, 0, rgba(0, 0, 0, 170))
			}
		})
	})
}

// metaRow is the episode count and tags, separated by dots.
func metaRow(c *ui.Context, s hongguo.Series, size float32) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		if s.EpisodeLabel != "" {
			ui.Text(c, s.EpisodeLabel).FontSize(size-1).FontWeight(700).Padding(1, 6).Radius(3).
				Border(1, withAlpha(colorWhite, 120)).TextColor(colorWhite)
		}
		for i, tag := range firstN(s.Tags, 4) {
			if i > 0 {
				ui.Box(c).Size(4, 4).Radius(2).Background(colorFaint)
			}
			ui.Text(c, tag).FontSize(size).TextColor(rgba(220, 220, 220, 255))
		}
	})
}

func bigButton(c *ui.Context, key string, ico *ui.SVG, label string, primary bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Padding(10, 26, 10, 20).Radius(6).Gap(10).AlignItems(ui.Center).Label(label)
	fg := colorWhite
	switch {
	case primary && b.Hovered():
		b.Background(rgba(255, 255, 255, 200))
		fg = rgba(0, 0, 0, 255)
	case primary:
		b.Background(colorWhite)
		fg = rgba(0, 0, 0, 255)
	case b.Hovered():
		b.Background(rgba(109, 109, 110, 110))
	default:
		b.Background(colorGlass)
	}
	b.Children(func() {
		ui.Icon(c, ico).Size(20, 20).TextColor(fg)
		ui.Text(c, label).FontSize(16).FontWeight(700).TextColor(fg).SingleLine()
	})
	return b
}

// posterRow is a titled row of posters that scrolls sideways, with arrows
// at its ends while the pointer is over it.
func (a *app) posterRow(c *ui.Context, key, title string, f *feed, more func()) {
	ui.Column(c.Key("row-" + key)).Gap(6).Children(func() {
		head := ui.Row(c).Padding(0, pagePad).AlignItems(ui.End).Gap(10)
		head.Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(700)
			if more != nil {
				link := ui.ButtonBase(c.Key("more")).Row().AlignItems(ui.Center).Gap(2).Label("查看全部")
				col := map[bool]ui.Color{true: colorWhite, false: rgba(84, 185, 197, 255)}[link.Hovered()]
				link.Children(func() {
					ui.Text(c, "查看全部").FontSize(13).FontWeight(600).TextColor(col)
					ui.Icon(c, iconChevronRight).Size(13, 13).TextColor(col)
				})
				if link.Clicked() {
					more()
				}
			}
		})
		switch {
		case f.err != "":
			ui.Row(c).Padding(20, pagePad).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, f.err).TextColor(colorMuted).FontSize(13)
				if ui.Button(c.Key("retry"), "重试").Clicked() {
					a.loadFeed(key, 1)
				}
			})
			return
		case len(f.items) == 0:
			ui.Row(c).Padding(10, pagePad).Gap(cardGap).Children(func() {
				for i := range 7 {
					ui.Box(c.Key(i)).Size(cardW, cardW*4/3).Radius(6).Background(colorCard)
				}
			})
			return
		}
		st := a.scroll("row-" + key)
		box := ui.Box(c.Key("strip"))
		over := box.Hovered()
		box.Children(func() {
			ui.ScrollHorizontal(c).TrackScroll(st).Scrollbars(false).Padding(12, pagePad, 34, pagePad).Gap(cardGap).
				Children(func() {
					for _, s := range f.items {
						a.posterCard(c, s, cardW, "")
					}
					if f.more() {
						a.moreCard(c, key, f)
					}
				})
			w, _ := c.Size()
			arrow := func(name string, ico *ui.SVG, left bool, show bool) {
				b := ui.ButtonBase(c.Key(name)).Absolute().Top(12).Bottom(34).Width(pagePad - 6).Center().Label(name)
				if left {
					b.Left(0)
				} else {
					b.Right(0)
				}
				o := b.Animate("o", b2f(over && show), 150*time.Millisecond)
				b.Opacity(o).Background(rgba(20, 20, 20, 150+60*b2f(b.Hovered())))
				b.Children(func() { ui.Icon(c, ico).Size(26, 26).TextColor(colorWhite) })
				if b.Clicked() && show {
					dx := w - 2*pagePad - cardW
					if left {
						dx = -dx
					}
					st.ScrollTo(st.X+dx, 0, ui.Animation{Duration: 450 * time.Millisecond, Ease: ui.EaseInOut})
				}
			}
			arrow("向左", iconChevronLeft, true, st.X > 1)
			arrow("向右", iconChevronRight, false, st.X < st.MaxX-1)
		})
	})
}

// moreCard ends a row with a card loading its next page.
func (a *app) moreCard(c *ui.Context, key string, f *feed) {
	b := ui.ButtonBase(c.Key("more-card")).Size(cardW, cardW*4/3).Shrink(0).Radius(6).Center().Column().Gap(8).Label("加载更多")
	b.Background(map[bool]ui.Color{true: rgba(56, 56, 56, 255), false: colorCard}[b.Hovered()])
	b.Children(func() {
		if f.loading {
			ui.Spinner(c).FontSize(20)
			return
		}
		ui.Icon(c, iconChevronRight).Size(28, 28).TextColor(colorMuted)
		ui.Text(c, "加载更多").FontSize(13).TextColor(colorMuted)
	})
	if b.Clicked() && !f.loading {
		a.loadFeed(key, f.page+1)
	}
}

// posterCard is a poster that grows under the pointer, with its title
// below. note, when set, replaces the episode count, as "看到第 3 集".
func (a *app) posterCard(c *ui.Context, s hongguo.Series, width float32, note string) {
	card := ui.Column(c.Key(s.ID)).Width(width).Shrink(0).Gap(8).Cursor(ui.CursorPointer).Label(s.Title)
	hover := card.Hovered()
	k := card.AnimateTo("hover", b2f(hover), ui.Animation{Duration: 180 * time.Millisecond, Ease: ui.EaseOut})
	card.Scale(1+0.05*k, 1+0.05*k).Translate(0, -4*k)
	if card.Clicked() {
		a.open(s)
	}
	card.Children(func() {
		ui.Box(c).Width(width).Height(width*4/3).Radius(6).Clip().Background(colorCard).
			Shadow(0, 10*k, 28*k, 0, rgba(0, 0, 0, 160*k)).Children(func() {
			if cv := a.coverOf(s.Cover); cv != nil && cv.img != nil {
				ui.Image(c, cv.img).Fill().Fit(ui.Cover)
			} else {
				ui.Column(c).Fill().Center().Padding(12).Children(func() {
					ui.Text(c, s.Title).FontSize(13).TextColor(colorFaint).TextAlign(ui.Center).MaxLines(3)
				})
			}
			// The episode count over the poster's corner.
			if s.EpisodeLabel != "" && note == "" {
				ui.Text(c, s.EpisodeLabel).Absolute().Right(6).Bottom(6).FontSize(10).FontWeight(600).
					Padding(2, 6).Radius(4).Background(rgba(0, 0, 0, 170)).TextColor(colorWhite)
			}
			if hover {
				ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(6).Border(2, withAlpha(colorWhite, 200))
				ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Children(func() {
					ui.Box(c).Size(48, 48).Radius(24).Center().Background(rgba(0, 0, 0, 150)).
						Border(2, colorWhite).Children(func() {
						ui.Icon(c, iconPlayFill).Size(20, 20).TextColor(colorWhite).Margin(0, 0, 0, 3)
					})
				})
			}
		})
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, s.Title).FontSize(13).FontWeight(600).MaxLines(1).
				TextColor(map[bool]ui.Color{true: colorWhite, false: rgba(229, 229, 229, 255)}[hover])
			sub := note
			if sub == "" {
				sub = strings.Join(firstN(s.Tags, 3), " · ")
			}
			ui.Text(c, sub).FontSize(11).TextColor(colorFaint).SingleLine()
		})
	})
}

func (a *app) continueRow(c *ui.Context) {
	ui.Column(c.Key("row-continue")).Gap(6).Children(func() {
		ui.Text(c, "继续观看").FontSize(20).FontWeight(700).Padding(0, pagePad)
		ui.ScrollHorizontal(c).TrackScroll(a.scroll("row-continue")).Scrollbars(false).Padding(12, pagePad, 34, pagePad).Gap(cardGap).
			Children(func() {
				for _, w := range a.history {
					note := fmt.Sprintf("看到第 %d 集", w.Episode+1)
					if w.Total > 0 {
						note += fmt.Sprintf(" · 共 %d 集", w.Total)
					}
					a.posterCard(c, w.Series, cardW, note)
				}
			})
	})
}

// --- category and search ---

func (a *app) categoryPage(c *ui.Context, slug string) {
	f := a.ensureFeed(slug)
	label := slug
	for _, cat := range hongguo.Categories {
		if cat.Slug == slug {
			label = cat.Label
		}
	}
	if len(f.items) > 0 {
		a.heroBanner(c, f.items[0], nil)
	} else {
		a.heroPlaceholder(c, f)
	}
	ui.Column(c).Margin(-70, 0, 0, 0).Gap(16).Padding(0, pagePad, 56, pagePad).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "全部"+label).FontSize(22).FontWeight(700)
			if f.totalPages > 0 {
				ui.Textf(c, "已加载 %d 部", len(f.items)).FontSize(12).TextColor(colorFaint)
			}
		})
		a.posterGrid(c, slug, f)
	})
}

func (a *app) searchPage(c *ui.Context) {
	f := a.feed("search")
	ui.Column(c).Gap(18).Padding(navHeight+28, pagePad, 56, pagePad).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "“"+a.searched+"”").FontSize(28).FontWeight(800)
			ui.Text(c, "的搜索结果").FontSize(16).TextColor(colorMuted)
		})
		if !f.loading && f.err == "" && len(f.items) == 0 {
			ui.Column(c).Padding(80, 0).Center().Gap(10).Children(func() {
				ui.Icon(c, iconSearch).Size(40, 40).TextColor(colorFaint)
				ui.Text(c, "没有找到相关短剧，换个关键词试试").TextColor(colorMuted)
			})
			return
		}
		a.posterGrid(c, "search", f)
	})
}

// posterGrid lays the feed's posters out in as many columns as fit the
// window, with a button loading the next page.
func (a *app) posterGrid(c *ui.Context, key string, f *feed) {
	w, _ := c.Size()
	avail := w - 2*pagePad
	gap := float32(14)
	cols := max(2, int((avail+gap)/(cardW+gap)))
	cw := (avail - gap*float32(cols-1)) / float32(cols)
	if f.err != "" && len(f.items) == 0 {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, f.err).TextColor(colorMuted)
			if ui.Button(c.Key("retry"), "重试").Clicked() {
				a.loadFeed(key, 1)
			}
		})
		return
	}
	ui.Row(c).Wrap().GapX(gap).GapY(22).Padding(8, 0).Children(func() {
		for _, s := range f.items {
			a.posterCard(c, s, cw, "")
		}
	})
	ui.Row(c).Justify(ui.Center).Padding(12, 0).Children(func() {
		switch {
		case f.loading:
			ui.Spinner(c).FontSize(22)
		case f.err != "":
			if ui.Button(c.Key("retry-more"), "加载失败，重试").Clicked() {
				a.loadFeed(key, f.page+1)
			}
		case f.more():
			if bigButton(c, "load-more", iconChevronDown, "加载更多", false).Clicked() {
				a.loadFeed(key, f.page+1)
			}
		case len(f.items) > 0:
			ui.Text(c, "已经到底了").FontSize(12).TextColor(colorFaint)
		}
	})
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
