package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

const searchDebounceDelay = 350 * time.Millisecond

func (a *app) ensureHotSearch() []hongguo.HotQuery {
	if a.hotSearch == nil {
		l := &loaded[[]hongguo.HotQuery]{loading: true}
		a.hotSearch = l
		win := a.win
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			items, err := hongguo.HotSearch(ctx)
			if win != nil {
				win.Update(func() {
					l.loading = false
					if err != nil {
						l.err = err
						return
					}
					l.value = &items
				})
			}
		}()
	}
	if a.hotSearch.value != nil {
		return *a.hotSearch.value
	}
	return nil
}

func (a *app) fetchSuggestions(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		a.suggestSeq++
		a.suggestQuery = ""
		a.searchSuggestions = nil
		return
	}
	if prefix == a.suggestQuery {
		return
	}
	a.suggestQuery = prefix
	a.suggestSeq++
	seq := a.suggestSeq
	win := a.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		items, err := hongguo.Suggest(ctx, prefix)
		if err != nil || win == nil {
			return
		}
		win.Update(func() {
			if seq != a.suggestSeq {
				return
			}
			a.searchSuggestions = firstN(items, 8)
		})
	}()
}

func (a *app) onSearchInputChanged() {
	trimmed := strings.TrimSpace(a.query)
	a.fetchSuggestions(trimmed)
	if trimmed == "" {
		a.search("")
		return
	}
	a.searchDebounceQuery = trimmed
	a.searchDebounceAt = time.Now().Add(searchDebounceDelay)
}

func (a *app) driveSearchDebounce(c *ui.Context) {
	if a.searchDebounceQuery == "" {
		return
	}
	rem := time.Until(a.searchDebounceAt)
	if rem > 0 {
		c.After(rem)
		return
	}
	q := a.searchDebounceQuery
	a.searchDebounceQuery = ""
	a.searchDebounceAt = time.Time{}
	if q != "" && q != a.searched {
		a.search(q)
	}
}

func (a *app) pickSearchQuery(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	a.query = q
	a.fetchSuggestions(q)
	a.search(q)
}

// searchPage is a Netflix-inspired full-page search experience: a prominent
// dark search bar, live "explore related to" suggestions, trending searches and
// top-searches poster discovery when idle, and a responsive results grid.
func (a *app) searchPage(c *ui.Context) {
	a.driveSearchDebounce(c)
	ui.Column(c.Key("search-page")).Gap(24).Padding(navHeight+28, pagePad, 64, pagePad).Children(func() {
		a.searchHeroBar(c)

		if strings.TrimSpace(a.searched) == "" {
			a.searchDiscovery(c)
			return
		}

		a.searchRelatedRow(c)
		f := a.feed("search")
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "“"+a.searched+"”").FontSize(26).FontWeight(800)
			ui.Text(c, "的搜索结果").FontSize(15).TextColor(colorMuted)
			if len(f.items) > 0 {
				ui.Textf(c, "已加载 %d 部", len(f.items)).FontSize(12).TextColor(colorFaint)
			}
		})

		if !f.loading && f.err == "" && len(f.items) == 0 {
			a.searchEmptyState(c)
			return
		}
		a.posterGrid(c, "search", f)
	})
}

func (a *app) searchHeroBar(c *ui.Context) {
	focused := a.searchInput.Focused(c)
	borderCol := rgba(255, 255, 255, 36)
	bgCol := rgba(255, 255, 255, 14)
	if focused {
		borderCol = rgba(255, 255, 255, 185)
		bgCol = rgba(28, 28, 28, 255)
	}
	ui.Row(c.Key("search-bar-shell")).Height(56).Padding(0, 16, 0, 18).Radius(8).
		Gap(14).AlignItems(ui.Center).Background(bgCol).Border(1.5, borderCol).Children(func() {
		ui.Icon(c, iconSearch).Size(22, 22).TextColor(map[bool]ui.Color{true: colorWhite, false: colorMuted}[focused])
		ui.TextInputBase(c.Key("search-page-input"), &a.query).
			Bind(&a.searchInput).
			AutoFocus().
			Grow(1).
			FontSize(18).
			TextColor(colorWhite).
			Placeholder("搜索剧名、题材、演员…").
			Label("搜索短剧").
			OnChange(func() { a.onSearchInputChanged() }).
			OnSubmit(func() { a.search(a.query) })
		if a.query != "" {
			clearBtn := ui.ButtonBase(c.Key("search-clear")).Size(30, 30).Radius(15).Center().
				Label("清空搜索").Tooltip("清空搜索")
			clearBtn.Background(rgba(255, 255, 255, 20+28*b2f(clearBtn.Hovered())))
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(15, 15).TextColor(colorWhite)
			})
			if clearBtn.Clicked() {
				a.query = ""
				a.fetchSuggestions("")
				a.search("")
				a.searchInput.Focus(c)
			}
		}
	})
}

// searchRelatedRow mirrors Netflix's "Explore titles related to:" strip.
func (a *app) searchRelatedRow(c *ui.Context) {
	var terms []string
	cur := strings.TrimSpace(a.searched)
	for _, s := range a.searchSuggestions {
		t := strings.TrimSpace(s)
		if t == "" || t == cur {
			continue
		}
		terms = append(terms, t)
	}
	if len(terms) == 0 {
		return
	}
	ui.Row(c.Key("search-related")).Wrap().GapX(10).GapY(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "探索相关：").FontSize(14).TextColor(colorFaint)
		for i, term := range terms {
			if i > 0 {
				ui.Text(c, "|").FontSize(13).TextColor(rgba(255, 255, 255, 42))
			}
			t := term
			btn := ui.ButtonBase(c.Key(fmt.Sprintf("rel-%d", i))).Padding(2, 4).Label(t)
			col := map[bool]ui.Color{true: colorWhite, false: rgba(215, 215, 215, 255)}[btn.Hovered()]
			btn.Children(func() {
				ui.Text(c, t).FontSize(14).FontWeight(map[bool]int{true: 700, false: 500}[btn.Hovered()]).TextColor(col).SingleLine()
			})
			if btn.Clicked() {
				a.pickSearchQuery(t)
			}
		}
	})
}

// searchDiscovery shows trending queries and popular dramas before the user
// enters a search term, matching Netflix's idle search screen.
func (a *app) searchDiscovery(c *ui.Context) {
	a.searchRelatedRow(c)
	a.hotSearchSection(c)

	popular := a.searchPopularItems()
	if len(popular) > 0 {
		ui.Column(c.Key("search-popular")).Gap(12).Children(func() {
			ui.Text(c, "热门搜索推荐").FontSize(20).FontWeight(700)
			a.seriesGrid(c, firstN(popular, 24))
		})
	}
}

func (a *app) searchPopularItems() []hongguo.Series {
	if r := a.feed(rankKey(hongguo.Rankings[0].Slug)); len(r.items) > 0 {
		return r.items
	}
	if r := a.feed(hongguo.Categories[0].Slug); len(r.items) > 0 {
		return r.items
	}
	return nil
}

func (a *app) hotSearchSection(c *ui.Context) {
	hot := firstN(a.ensureHotSearch(), 12)
	if len(hot) == 0 {
		return
	}
	ui.Column(c.Key("hot-search-sec")).Gap(12).Children(func() {
		ui.Text(c, "大家都在搜").FontSize(18).FontWeight(700)
		ui.Row(c.Key("hot-search")).Wrap().GapX(10).GapY(10).AlignItems(ui.Center).Children(func() {
			for i, h := range hot {
				title := h.Title
				chip := ui.ButtonBase(c.Key(fmt.Sprintf("hot-%d", i))).
					Row().AlignItems(ui.Center).Gap(8).
					Padding(8, 14).Radius(6).Label(title)
				hover := chip.Hovered()
				chip.Background(rgba(255, 255, 255, 16+22*b2f(hover))).
					Border(1, rgba(255, 255, 255, 20+40*b2f(hover)))
				chip.Children(func() {
					if i < 3 {
						ui.Textf(c, "%d", i+1).FontSize(13).FontWeight(800).TextColor(colorRed)
					}
					ui.Text(c, title).FontSize(13).FontWeight(500).
						TextColor(map[bool]ui.Color{true: colorWhite, false: rgba(224, 224, 224, 255)}[hover]).SingleLine()
				})
				if chip.Clicked() {
					a.pickSearchQuery(title)
				}
			}
		})
	})
}

func (a *app) searchEmptyState(c *ui.Context) {
	ui.Column(c.Key("search-empty")).Padding(48, 0, 24, 0).Gap(16).Children(func() {
		ui.Column(c).Center().Gap(10).Children(func() {
			ui.Icon(c, iconSearch).Size(44, 44).TextColor(colorFaint)
			ui.Textf(c, "未找到与“%s”相关的短剧", a.searched).FontSize(18).FontWeight(700)
			ui.Text(c, "建议：尝试使用更简短的片名、题材关键词或主角名称").FontSize(13).TextColor(colorMuted)
		})
		a.hotSearchSection(c)
	})
}
