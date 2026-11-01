// smovie is a native player for Hongguo short dramas, laid out as a
// streaming service: a featured banner and rows of posters, category and
// search pages, and a player with the episode list. The interface is
// MyGo's native UI; only the <video> plays in a web view, as native UI has
// no codecs or sound.
package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

const navHeight = 64

// feed is a list of series loaded page by page.
type feed struct {
	items      []hongguo.Series
	page       int
	totalPages int
	loading    bool
	err        string
	seq        int
}

func (f *feed) more() bool { return !f.loading && f.page < f.totalPages }

type cover struct {
	img    ui.ImageSource
	failed bool
}

type app struct {
	win    *mygo.Window
	player *mygo.WebView
	server *mediaServer
	lib    *library
	theme  *ui.Theme

	page     string // "home", a category slug, "search" or "watch"
	backTo   string // the page "watch" returns to
	query    string
	searched string

	feeds   map[string]*feed // by category slug, and "search"
	scrolls map[string]*ui.ScrollState
	hero    int // which featured series the home banner shows
	heroAt  time.Time

	coverMu   sync.Mutex
	covers    map[string]*cover
	coverWant map[string]bool
	coverSem  chan struct{}

	history []watched

	// What plays.
	series    *hongguo.Series
	detail    *hongguo.Detail
	detailErr string
	detailSeq int
	current   int // episode index
	job       *job
	playing   bool // the player page is loaded with current
	playSeq   int
	autoNext  bool
	epRange   int // which block of 50 episodes the list shows
}

func newApp() *app {
	return &app{
		page:      "home",
		backTo:    "home",
		feeds:     map[string]*feed{},
		scrolls:   map[string]*ui.ScrollState{},
		covers:    map[string]*cover{},
		coverWant: map[string]bool{},
		coverSem:  make(chan struct{}, 6),
		lib:       newLibrary(),
		theme:     netflixTheme(),
		history:   loadHistory(),
		autoNext:  true,
		current:   -1,
		heroAt:    time.Now(),
	}
}

func (a *app) feed(key string) *feed {
	f, ok := a.feeds[key]
	if !ok {
		f = &feed{}
		a.feeds[key] = f
	}
	return f
}

func (a *app) scroll(key string) *ui.ScrollState {
	s, ok := a.scrolls[key]
	if !ok {
		s = &ui.ScrollState{}
		a.scrolls[key] = s
	}
	return s
}

// loadFeed loads page of a feed; a page after the first is added to it.
func (a *app) loadFeed(key string, page int) {
	f := a.feed(key)
	f.seq++
	seq := f.seq
	f.loading, f.err = true, ""
	query := a.searched
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var p *hongguo.Page
		var err error
		if key == "search" {
			p, err = hongguo.Search(ctx, query, page)
		} else {
			p, err = hongguo.Browse(ctx, key, page)
		}
		a.win.Update(func() {
			if seq != f.seq {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			if page <= 1 {
				f.items = nil
			}
			seen := map[string]bool{}
			for _, s := range f.items {
				seen[s.ID] = true
			}
			for _, s := range p.Items {
				if !seen[s.ID] {
					f.items = append(f.items, s)
				}
			}
			f.page, f.totalPages = p.Page, p.TotalPages
		})
	}()
}

// ensureFeed loads the first page of a feed once.
func (a *app) ensureFeed(key string) *feed {
	f := a.feed(key)
	if f.page == 0 && !f.loading && f.err == "" {
		a.loadFeed(key, 1)
	}
	return f
}

func (a *app) search(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	a.searched = q
	a.feed("search").items = nil
	a.loadFeed("search", 1)
	a.go_("search")
}

// go_ shows a page.
func (a *app) go_(page string) {
	if page != "watch" {
		a.backTo = page
	}
	a.page = page
}

// coverOf returns a poster, starting its download the first time it shows.
func (a *app) coverOf(url string) *cover {
	if url == "" {
		return nil
	}
	a.coverMu.Lock()
	defer a.coverMu.Unlock()
	if c, ok := a.covers[url]; ok {
		return c
	}
	if a.coverWant[url] {
		return nil
	}
	a.coverWant[url] = true
	go func() {
		a.coverSem <- struct{}{}
		defer func() { <-a.coverSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := &cover{}
		data, err := hongguo.FetchImage(ctx, url)
		if err == nil {
			c.img, err = ui.DecodeImage(data)
		}
		c.failed = err != nil
		a.coverMu.Lock()
		a.covers[url] = c
		a.coverMu.Unlock()
		a.win.Invalidate()
	}()
	return nil
}

func (a *app) lastEpisode(id string) (int, bool) {
	for _, w := range a.history {
		if w.Series.ID == id {
			return w.Episode, true
		}
	}
	return 0, false
}

// --- playback ---

// open shows a series and plays it from where the user left it.
func (a *app) open(s hongguo.Series) {
	if a.page != "watch" {
		a.backTo = a.page
	}
	a.page = "watch"
	if a.series != nil && a.series.ID == s.ID {
		return
	}
	start, _ := a.lastEpisode(s.ID)
	a.series = &s
	a.detail, a.detailErr = nil, ""
	a.current, a.job, a.playing = -1, nil, false
	a.epRange = start / 50
	a.player.Page().LoadHTML(blankHTML, "")
	a.scroll("watch").Y = 0
	a.detailSeq++
	seq := a.detailSeq
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		d, err := hongguo.Episodes(ctx, s.ID)
		a.win.Update(func() {
			if seq != a.detailSeq {
				return
			}
			if err != nil {
				a.detailErr = err.Error()
				return
			}
			if d.Intro == "" {
				d.Intro = s.Intro
			}
			if len(d.Tags) == 0 {
				d.Tags = s.Tags
			}
			a.detail = d
			a.play(min(start, len(d.VIDs)-1))
		})
	}()
}

func (a *app) play(index int) {
	if a.detail == nil || index < 0 || index >= len(a.detail.VIDs) {
		return
	}
	a.current = index
	a.epRange = index / 50
	a.playing = false
	a.playSeq++
	seq := a.playSeq
	vid := a.detail.VIDs[index]
	total := len(a.detail.VIDs)
	a.history = recordWatch(a.history, a.detail.Series, index, total)
	saveHistory(append([]watched(nil), a.history...))
	a.player.Page().LoadHTML(blankHTML, "")
	j := a.lib.ensure(vid)
	a.job = j
	go func() {
		<-j.done
		a.win.Update(func() {
			if seq != a.playSeq || j.err != nil {
				return
			}
			a.playing = true
			_ = a.player.Page().LoadURL(a.server.playerURL(vid, index+1, total))
			// Prefetch the next episode so continuous play does not wait.
			if next := index + 1; a.detail != nil && next < len(a.detail.VIDs) {
				a.lib.ensure(a.detail.VIDs[next])
			}
		})
	}()
}

// step plays the episode before (-1) or after (+1) vid, when vid still
// plays: from the player's buttons and when an episode ends.
func (a *app) step(vid string, delta int, ended bool) {
	a.win.Update(func() {
		if a.detail == nil || a.current < 0 || a.detail.VIDs[a.current] != vid {
			return
		}
		if ended && !a.autoNext {
			return
		}
		a.play(a.current + delta)
	})
}

func main() {
	a := newApp()
	srv, err := startMediaServer(a.lib, a.step)
	if err != nil {
		log.Fatal(err)
	}
	a.server = srv
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "红果短剧",
			Width:           1280,
			Height:          820,
			MinWidth:        900,
			MinHeight:       600,
			StateKey:        "main",
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  navHeight,
			BackgroundColor: "#141414",
			Content:         ui.View(a.view),
		})
		a.player, err = a.win.NewWebView(mygo.WebViewOptions{
			BackgroundColor: "#000000",
			Page: mygo.PageOptions{
				Autoplay:         mygo.AutoplayAllow,
				PictureInPicture: true,
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		a.player.Page().LoadHTML(blankHTML, "")
		for _, cat := range hongguo.Categories {
			a.ensureFeed(cat.Slug)
		}
		a.win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
