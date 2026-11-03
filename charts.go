package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// The genres a category narrows to, and the site's charts: a Top 10 row
// on the home page, and a page with each chart in full.

var colorChipOn = rgba(229, 9, 20, 46)

// chip is a choice among a few, as a genre or a chart: plain text, and
// tinted red when chosen.
func chip(c *ui.Context, key, label string, on bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 16).Radius(8).Center().Label(label)
	hover := b.Hovered()
	switch {
	case on:
		b.Background(colorChipOn)
	case hover:
		b.Background(rgba(255, 255, 255, 22))
	}
	b.Children(func() {
		col := map[bool]ui.Color{true: colorWhite, false: rgba(200, 200, 200, 255)}[hover]
		if on {
			col = rgba(255, 120, 128, 255)
		}
		ui.Text(c, label).FontSize(14).FontWeight(map[bool]int{true: 700, false: 500}[on]).TextColor(col).SingleLine()
	})
	return b
}

// genreChips narrows a category to one of its genres, the site's own.
func (a *app) genreChips(c *ui.Context, cat string, genres []hongguo.Genre) {
	if len(genres) == 0 {
		return
	}
	cur := a.genre[cat]
	ui.Row(c.Key("genres")).Wrap().GapX(6).GapY(8).Margin(0, -16, 4, -16).Children(func() {
		if chip(c, "genre-all", "全部", cur == "").Clicked() {
			a.genre[cat] = ""
		}
		for _, g := range genres {
			if chip(c, "genre-"+g.Slug, g.Label, cur == g.Slug).Clicked() && cur != g.Slug {
				a.genre[cat] = g.Slug
			}
		}
	})
}

// categoryFeed is the list a category page shows: all of it, or the
// genre chosen.
func (a *app) categoryFeed(cat string) (string, *feed) {
	key := cat
	if g := a.genre[cat]; g != "" {
		key = cat + "/" + g
	}
	return key, a.ensureFeed(key)
}

func genreLabel(genres []hongguo.Genre, slug string) string {
	for _, g := range genres {
		if g.Slug == slug {
			return g.Label
		}
	}
	return slug
}

// --- charts ---

func boardLabel(slug string) string {
	for _, r := range hongguo.Rankings {
		if r.Slug == slug {
			return r.Label
		}
	}
	return slug
}

// boardChips chooses the chart to show.
func (a *app) boardChips(c *ui.Context) {
	ui.Row(c.Key("boards")).Gap(4).AlignItems(ui.Center).Children(func() {
		for _, r := range hongguo.Rankings {
			if chip(c, "board-"+r.Slug, r.Label, a.board == r.Slug).Clicked() {
				a.board = r.Slug
			}
		}
	})
}

const top10W = 150 // the poster's width in the Top 10 row

// rankRow is the chart chosen on the home page: its ten first, each with
// its place in large figures beside its poster.
func (a *app) rankRow(c *ui.Context) {
	key := rankKey(a.board)
	f := a.ensureFeed(key)
	ui.Column(c.Key("row-rank")).Gap(6).Children(func() {
		ui.Row(c).Padding(0, pagePad).AlignItems(ui.Center).Gap(16).Wrap().Children(func() {
			ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
				ui.Text(c, "热播榜").FontSize(20).FontWeight(700)
				if linkButton(c, "rank-more", "完整榜单").Clicked() {
					a.go_("rank")
					a.scroll("rank").Y = 0
				}
			})
			a.boardChips(c)
		})
		switch {
		case f.err != "" && len(f.items) == 0:
			ui.Row(c).Padding(20, pagePad).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, f.err).TextColor(colorMuted).FontSize(13)
				if ui.Button(c.Key("retry"), "重试").Clicked() {
					a.loadFeed(key, 1)
				}
			})
			return
		case len(f.items) == 0:
			ui.Row(c).Padding(12, pagePad, 34, pagePad).Gap(cardGap).Children(func() {
				for i := range 6 {
					ui.Box(c.Key(i)).Size(top10W+70, top10W*4/3).Radius(6).Background(colorCard)
				}
			})
			return
		}
		st := a.scroll("row-" + key)
		ui.ScrollHorizontal(c.Key("strip-"+key)).TrackScroll(st).Scrollbars(false).HandleInput(a.rowScroll(st)).
			Padding(12, pagePad, 34, pagePad).Gap(18).Children(func() {
			for i, s := range firstN(f.items, 10) {
				a.top10Card(c, i+1, s)
			}
		})
	})
}

// top10Card is a series of the chart's ten first: its place, large and
// outlined in the manner of streaming services, with its poster over the
// figure's right edge.
func (a *app) top10Card(c *ui.Context, rank int, s hongguo.Series) {
	numW := float32(74)
	if rank >= 10 {
		numW = 128
	}
	card := ui.Row(c.Key("top-" + s.ID)).Shrink(0).AlignItems(ui.End).Cursor(ui.CursorPointer).Label(fmt.Sprintf("第 %d 名 %s", rank, s.Title))
	hover := card.Hovered()
	k := card.AnimateTo("hover", b2f(hover), ui.Animation{Duration: 180 * time.Millisecond, Ease: ui.EaseOut})
	if card.Clicked() {
		a.open(s)
	}
	card.Children(func() {
		figure := fmt.Sprint(rank)
		ui.Box(c).Width(numW).Height(top10W * 4 / 3).Shrink(0).Children(func() {
			// The outline: the figure in grey a little out on each side,
			// under the figure in the page's color.
			for _, d := range [][2]float32{{-2, 0}, {2, 0}, {0, -2}, {0, 2}, {-1.5, -1.5}, {1.5, 1.5}, {-1.5, 1.5}, {1.5, -1.5}} {
				ui.Text(c, figure).Absolute().Right(-8 + d[0]).Bottom(-18 + d[1]).FontSize(150).FontWeight(900).LetterSpacing(-14).
					TextColor(rgba(110, 110, 110, 200+55*k)).SingleLine()
			}
			ui.Text(c, figure).Absolute().Right(-8).Bottom(-18).FontSize(150).FontWeight(900).LetterSpacing(-14).
				TextColor(colorBG).SingleLine()
		})
		ui.Box(c).Width(top10W).Height(top10W*4/3).Shrink(0).Radius(6).Clip().Background(colorCard).
			Scale(1+0.04*k, 1+0.04*k).Shadow(0, 10*k, 28*k, 0, rgba(0, 0, 0, 160*k)).Children(func() {
			pic := a.images.Get(c, s.Cover, imgSmall, card.InView())
			if pic.Img == nil || pic.Fade < 1 {
				posterFallback(c, s.Title, pic.Failed)
			}
			if pic.Img != nil {
				ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
			}
			if s.Heat != "" {
				ui.Text(c, s.Heat).Absolute().Left(0).Right(0).Bottom(0).Padding(18, 8, 7, 8).FontSize(11).FontWeight(700).
					TextColor(colorWhite).SingleLine().
					LinearGradient(ui.LinearGradient{Angle: 180, From: rgba(0, 0, 0, 0), To: rgba(0, 0, 0, 200)})
			}
			if hover {
				ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(6).Border(2, withAlpha(colorWhite, 200))
			}
		})
	})
}

// rankPage shows a chart in full, a hundred series, as rows with their
// place, poster, tags, heat and synopsis.
func (a *app) rankPage(c *ui.Context) {
	key := rankKey(a.board)
	f := a.ensureFeed(key)
	ui.Column(c).Gap(18).Padding(navHeight+28, pagePad, 56, pagePad).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(20).Wrap().Children(func() {
			ui.Text(c, "排行榜").FontSize(28).FontWeight(800)
			a.boardChips(c)
		})
		ui.Text(c, boardLabel(a.board)+"按红果官网热度排序").FontSize(12).TextColor(colorFaint)
		if f.err != "" && len(f.items) == 0 {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, f.err).TextColor(colorMuted)
				if ui.Button(c.Key("retry"), "重试").Clicked() {
					a.loadFeed(key, 1)
				}
			})
			return
		}
		w, _ := c.Size()
		avail := w - 2*pagePad
		cols := 1
		if avail >= 1100 {
			cols = 2
		}
		const gap = 14
		rw := float32(math.Floor(float64((avail-gap*float32(cols-1))/float32(cols)))) - 0.5
		ui.Row(c.Key("rank-list-" + a.board)).Wrap().Gap(gap).Children(func() {
			for i, s := range f.items {
				a.rankEntry(c, i+1, s, rw)
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
				ui.Text(c, fmt.Sprintf("已显示榜单前 %d 名", len(f.items))).FontSize(12).TextColor(colorFaint)
			}
		})
	})
}

// rankEntry is a row of the full chart.
func (a *app) rankEntry(c *ui.Context, rank int, s hongguo.Series, width float32) {
	row := ui.ButtonBase(c.Key("rank-" + s.ID)).Width(width).Padding(12).Radius(12).Gap(16).AlignItems(ui.Center).
		Label(fmt.Sprintf("第 %d 名 %s", rank, s.Title))
	hover := row.Hovered()
	row.Background(rgba(255, 255, 255, 10+12*b2f(hover)))
	row.Children(func() {
		medal := map[int]ui.Color{1: rgba(255, 196, 0, 255), 2: rgba(200, 205, 215, 255), 3: rgba(222, 141, 82, 255)}
		col, top := medal[rank]
		if !top {
			col = colorMuted
		}
		ui.Text(c, fmt.Sprint(rank)).Width(44).Shrink(0).TextAlign(ui.Center).FontSize(map[bool]float32{true: 30, false: 22}[top]).
			FontWeight(900).TextColor(col).SingleLine()
		ui.Box(c).Size(84, 112).Shrink(0).Radius(8).Clip().Background(colorCard).Children(func() {
			pic := a.images.Get(c, s.Cover, imgSmall, row.InView())
			if pic.Img == nil || pic.Fade < 1 {
				posterFallback(c, s.Title, pic.Failed)
			}
			if pic.Img != nil {
				ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, s.Title).FontSize(16).FontWeight(700).SingleLine()
			var meta []string
			if s.Heat != "" {
				meta = append(meta, s.Heat)
			}
			if s.EpisodeLabel != "" {
				meta = append(meta, s.EpisodeLabel)
			}
			if len(s.Tags) > 0 {
				meta = append(meta, strings.Join(firstN(s.Tags, 3), " · "))
			}
			if w, ok := a.watchOf(s.ID); ok {
				meta = append(meta, watchNote(w))
			}
			ui.Text(c, strings.Join(meta, "  ·  ")).FontSize(12).TextColor(colorMuted).SingleLine()
			if s.Intro != "" {
				ui.Text(c, s.Intro).FontSize(13).TextColor(rgba(200, 200, 200, 255)).MaxLines(2).LineHeight(1.45)
			}
		})
		if hover {
			ui.Box(c).Size(40, 40).Shrink(0).Radius(20).Center().Background(colorWhite).Children(func() {
				ui.Icon(c, iconPlayFill).Size(18, 18).TextColor(rgba(15, 15, 15, 255)).Margin(0, 0, 0, 2)
			})
		}
	})
	if row.Clicked() {
		a.open(s)
	}
}
