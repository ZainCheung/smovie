package main

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// showDetails opens the details dialog of a series over the page, as
// streaming services do for More Info, rather than leave the page.
func (a *app) showDetails(s hongguo.Series) {
	a.detailsOpen = true
	a.detailsOf = s
	a.details, a.detailsErr = nil, ""
	ep, _ := a.lastEpisode(s.ID)
	a.detailsRange = ep / epBlock
	a.scroll("details").Y = 0
	a.detailsSeq++
	seq := a.detailsSeq
	a.episodes(s, func(d *hongguo.Detail, err error) {
		if seq != a.detailsSeq {
			return
		}
		if err != nil {
			a.detailsErr = err.Error()
			return
		}
		a.details = d
	})
}

func (a *app) detailsDialog(c *ui.Context) {
	if !a.detailsOpen {
		return
	}
	w, h := c.Size()
	pw := min(w-80, 940)
	s := a.detailsOf
	if a.details != nil {
		s = a.details.Series
	}
	ui.DialogBase(c.Key("details"), &a.detailsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(rgba(0, 0, 0, 175))
		panel.Width(pw).MaxHeight(h-48).Radius(12).Clip().Background(rgba(24, 24, 24, 255)).
			Shadow(0, 24, 70, 0, rgba(0, 0, 0, 200)).Label(s.Title)
		ui.Scroll(c.Key("details-scroll")).MaxHeight(h - 48).TrackScroll(a.scroll("details")).Children(func() {
			a.detailsHeader(c, s, pw)
			ui.Column(c).Padding(8, 32, 36, 32).Gap(26).Children(func() {
				a.detailsEpisodes(c, s)
				a.moreLikeThis(c, s, pw-64)
			})
		})
		close := ui.ButtonBase(c.Key("details-close")).Absolute().Top(14).Right(14).Size(36, 36).Radius(18).Center().
			Label("关闭").Tooltip("关闭 (Esc)")
		close.Background(rgba(24, 24, 24, 200+55*b2f(close.Hovered())))
		close.Children(func() { ui.Icon(c, iconClose).Size(18, 18).TextColor(colorWhite) })
		if close.Clicked() {
			a.detailsOpen = false
		}
	})
}

func (a *app) detailsHeader(c *ui.Context, s hongguo.Series, pw float32) {
	const hh = 400
	backdrop := a.images.Get(c, s.Cover, imgSmall, true)
	poster := a.images.Get(c, s.Cover, imgLarge, true)
	ui.Column(c).Children(func() {
		ui.Box(c).Height(hh).Clip().Children(func() {
			if backdrop.Img != nil {
				ui.Image(c, backdrop.Img).Absolute().Left(-50).Top(-50).Size(pw+100, hh+100).Fit(ui.Cover).Blur(40).Opacity(0.6 * backdrop.Fade)
			}
			ui.Box(c).Absolute().Left(0).Top(0).Size(pw, hh).LinearGradient(ui.LinearGradient{
				Angle: 180, Stops: []ui.GradientStop{{Color: withAlpha(colorBG, 60), At: 0}, {Color: rgba(24, 24, 24, 255), At: 1}}})
		})
		ui.Row(c).Margin(-hh, 0, 0, 0).Height(hh).Padding(40, 32, 24, 32).Gap(28).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(240, 320).Shrink(0).Radius(10).Clip().Background(colorCard).Shadow(0, 14, 40, 0, rgba(0, 0, 0, 170)).Children(func() {
				if poster.Img == nil || poster.Fade < 1 {
					posterFallback(c, s.Title, poster.Failed)
				}
				if poster.Img != nil {
					ui.Image(c, poster.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(poster.Fade)
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(14).Children(func() {
				ui.Text(c, s.Title).FontSize(30).FontWeight(800).LineHeight(1.2).MaxLines(3)
				metaRow(c, s, 13)
				if s.Intro != "" {
					ui.Text(c, s.Intro).FontSize(14).TextColor(rgba(220, 220, 220, 255)).LineHeight(1.6).MaxLines(6).Selectable()
				}
				ui.Row(c).Gap(12).AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
					label := "播放"
					if w, ok := a.watchOf(s.ID); ok {
						label = fmt.Sprintf("继续第 %d 集", w.Episode+1)
						if w.Finished {
							label = "重新播放"
						}
					}
					if bigButton(c, "details-play", iconPlayFill, label, true).Clicked() {
						a.open(s)
					}
					a.myListButton(c, "details-list", s, 44)
				})
			})
		})
	})
}

// detailsEpisodes lists the episodes, to start at any, marking those
// watched.
func (a *app) detailsEpisodes(c *ui.Context, s hongguo.Series) {
	ui.Column(c.Key("details-eps")).Gap(12).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "选集").FontSize(20).FontWeight(700)
			if a.details != nil {
				ui.Textf(c, "共 %d 集", len(a.details.VIDs)).FontSize(13).TextColor(colorFaint)
			}
		})
		switch {
		case a.detailsErr != "":
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.detailsErr).FontSize(13).TextColor(colorMuted)
				if ui.Button(c.Key("details-retry"), "重试").Clicked() {
					a.showDetails(a.detailsOf)
				}
			})
			return
		case a.details == nil:
			ui.Spinner(c).FontSize(20)
			return
		}
		n := len(a.details.VIDs)
		last, watchedAny := -1, false
		if w, ok := a.watchOf(s.ID); ok {
			last, watchedAny = w.Episode, true
			if w.Finished {
				last = n
			}
		}
		if blocks := (n + epBlock - 1) / epBlock; blocks > 1 {
			ui.Row(c).Wrap().Gap(8).Children(func() {
				for b := range blocks {
					lo, hi := b*epBlock+1, min((b+1)*epBlock, n)
					active := b == a.detailsRange
					pill := ui.ButtonBase(c.Key(fmt.Sprint("dblk", b))).Padding(5, 14).Radius(14).Label(fmt.Sprintf("%d-%d", lo, hi))
					pill.Background(map[bool]ui.Color{true: colorWhite, false: rgba(255, 255, 255, 18+22*b2f(pill.Hovered()))}[active])
					pill.Children(func() {
						ui.Textf(c, "%d-%d", lo, hi).FontSize(12).FontWeight(600).
							TextColor(map[bool]ui.Color{true: rgba(0, 0, 0, 255), false: rgba(220, 220, 220, 255)}[active])
					})
					if pill.Clicked() {
						a.detailsRange = b
					}
				}
			})
		}
		lo, hi := a.detailsRange*epBlock, min((a.detailsRange+1)*epBlock, n)
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for i := lo; i < hi; i++ {
				ep := ui.ButtonBase(c.Key(fmt.Sprint("dep", i))).Size(58, 40).Radius(6).Center().Label(fmt.Sprintf("第 %d 集", i+1))
				cur := watchedAny && i == last
				switch {
				case cur:
					ep.Background(rgba(229, 9, 20, 60)).Border(1, colorRed)
				case ep.Hovered():
					ep.Background(rgba(255, 255, 255, 46))
				default:
					ep.Background(rgba(255, 255, 255, 16))
				}
				ep.Children(func() {
					col := rgba(230, 230, 230, 255)
					if i < last && !cur {
						col = colorFaint // watched
					}
					ui.Textf(c, "%d", i+1).FontSize(14).FontWeight(600).TextColor(col)
				})
				if ep.Clicked() {
					a.openAt(s, i)
				}
			}
		})
	})
}

// moreLikeThis shows the series sharing the most tags with s among those
// loaded.
func (a *app) moreLikeThis(c *ui.Context, s hongguo.Series, width float32) {
	similar := a.similar(s, 12)
	if len(similar) == 0 {
		return
	}
	ui.Column(c.Key("details-similar")).Gap(12).Children(func() {
		ui.Text(c, "更多类似").FontSize(20).FontWeight(700)
		gap := float32(12)
		cols := max(3, int((width+gap)/(150+gap)))
		cw := (width-gap*float32(cols-1))/float32(cols) - 0.5
		ui.Row(c).Wrap().GapX(gap).GapY(18).Children(func() {
			for _, o := range similar {
				a.posterCard(c, o, cw, "", &cardExtra{open: func() { a.showDetails(o) }})
			}
		})
	})
}

// similar ranks the series known, from the categories, the history and
// My List, by the tags they share with s.
func (a *app) similar(s hongguo.Series, n int) []hongguo.Series {
	tags := map[string]bool{}
	for _, t := range s.Tags {
		tags[t] = true
	}
	if a.details != nil && a.details.ID == s.ID {
		for _, t := range a.details.Tags {
			tags[t] = true
		}
	}
	if len(tags) == 0 {
		return nil
	}
	type scored struct {
		s     hongguo.Series
		score float64
		order int
	}
	seen := map[string]bool{s.ID: true}
	var all []scored
	add := func(o hongguo.Series) {
		if seen[o.ID] {
			return
		}
		seen[o.ID] = true
		shared := 0
		for _, t := range o.Tags {
			if tags[t] {
				shared++
			}
		}
		if shared == 0 {
			return
		}
		union := len(tags) + len(o.Tags) - shared
		all = append(all, scored{o, float64(shared) / float64(union), len(all)})
	}
	for _, cat := range hongguo.Categories {
		for _, o := range a.feed(cat.Slug).items {
			add(o)
		}
	}
	for _, w := range a.history {
		add(w.Series)
	}
	for _, l := range a.myList {
		add(l.Series)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	out := make([]hongguo.Series, 0, n)
	for _, x := range firstN(all, n) {
		out = append(out, x.s)
	}
	return out
}
