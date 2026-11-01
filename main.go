// smovie is a minimal native player for Hongguo short dramas: a sidebar of
// catalog sections and search, a grid of posters, and a player with the
// episode list. The interface is MyGo's native UI; only the <video> plays in
// a web view, as native UI has no codecs or sound.
package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

const (
	titleBarHeight = 52
	sidebarWidth   = 210
)

type listState struct {
	items      []hongguo.Series
	page       int
	totalPages int
	loading    bool
	err        string
	seq        int
}

type cover struct {
	img    ui.ImageSource
	failed bool
}

type app struct {
	win    *mygo.Window
	player *mygo.WebView
	server *mediaServer
	lib    *library

	section  string // a category slug, "search" or "watch"
	lastList string // the list shown before "watch"
	query    string
	searched string

	list listState
	grid ui.GridState

	coverMu   sync.Mutex
	covers    map[string]*cover
	coverSem  chan struct{}
	coverWant map[string]bool

	// What plays.
	series     *hongguo.Series
	detail     *hongguo.Detail
	detailErr  string
	detailSeq  int
	current    int // episode index
	job        *job
	playing    bool // the player page is loaded with current
	playSeq    int
	autoNext   bool
	episodeSel int
}

func newApp() *app {
	return &app{
		section:   hongguo.Categories[0].Slug,
		lastList:  hongguo.Categories[0].Slug,
		covers:    map[string]*cover{},
		coverWant: map[string]bool{},
		coverSem:  make(chan struct{}, 6),
		lib:       newLibrary(),
		autoNext:  true,
		current:   -1,
	}
}

// --- lists ---

func (a *app) loadList(page int) {
	a.list.seq++
	seq := a.list.seq
	section, query := a.section, a.searched
	a.list.loading, a.list.err = true, ""
	if page <= 1 {
		a.list.items = nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var p *hongguo.Page
		var err error
		if section == "search" {
			p, err = hongguo.Search(ctx, query, page)
		} else {
			p, err = hongguo.Browse(ctx, section, page)
		}
		a.win.Update(func() {
			if seq != a.list.seq {
				return
			}
			a.list.loading = false
			if err != nil {
				a.list.err = err.Error()
				return
			}
			a.list.items, a.list.page, a.list.totalPages = p.Items, p.Page, p.TotalPages
			a.grid.ScrollTo(0, ui.Start)
		})
	}()
}

// coverOf returns a poster, starting its download the first time the grid
// shows it.
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

// --- playback ---

func (a *app) open(s hongguo.Series) {
	if a.series != nil && a.series.ID == s.ID {
		a.section = "watch"
		return
	}
	a.series = &s
	a.detail, a.detailErr = nil, ""
	a.current, a.job, a.playing = -1, nil, false
	a.player.Page().LoadHTML(blankHTML, "")
	if a.section != "watch" {
		a.lastList = a.section
	}
	a.section = "watch"
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
			a.detail = d
			a.play(0)
		})
	}()
}

func (a *app) play(index int) {
	if a.detail == nil || index < 0 || index >= len(a.detail.VIDs) {
		return
	}
	a.current = index
	a.playing = false
	a.playSeq++
	seq := a.playSeq
	vid := a.detail.VIDs[index]
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
			_ = a.player.Page().LoadURL(a.server.playerURL(vid))
			// Prefetch the next episode so continuous play does not wait.
			if next := index + 1; a.detail != nil && next < len(a.detail.VIDs) {
				a.lib.ensure(a.detail.VIDs[next])
			}
		})
	}()
}

func (a *app) ended(vid string) {
	a.win.Update(func() {
		if !a.autoNext || a.detail == nil || a.current < 0 || a.detail.VIDs[a.current] != vid {
			return
		}
		a.play(a.current + 1)
	})
}

func main() {
	a := newApp()
	srv, err := startMediaServer(a.lib, a.ended)
	if err != nil {
		log.Fatal(err)
	}
	a.server = srv
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "红果短剧",
			Width:          1180,
			Height:         780,
			MinWidth:       820,
			MinHeight:      560,
			StateKey:       "main",
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: titleBarHeight,
			Vibrancy:       mygo.VibrancySidebar,
			Content:        ui.View(a.view),
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
		a.loadList(1)
		a.win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
