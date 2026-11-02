// smovie is a native player for Hongguo short dramas, laid out as a
// streaming service: a featured banner and rows of posters, category and
// search pages, and a player with the episode list. The interface is
// MyGo's native UI; only the <video> plays in a web view, as native UI has
// no codecs or sound.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
	loadedAt   time.Time // of the first page, to refresh it once old
}

func (f *feed) more() bool { return !f.loading && f.page < f.totalPages }

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

	images *imageCache

	history      []watched
	confirmClear bool // asking whether to empty the history

	myList       []listed
	episodeCache map[string]*hongguo.Detail // by series ID

	// The details dialog.
	detailsOpen  bool
	detailsOf    hongguo.Series
	details      *hongguo.Detail
	detailsErr   string
	detailsSeq   int
	detailsRange int

	saved      settings // as last saved
	videoCache int      // index of videoCacheSizes
	usage      usage

	// What plays.
	series    *hongguo.Series
	detail    *hongguo.Detail
	detailErr string
	detailSeq int
	current   int // episode index
	job       *job
	playSeq   int
	aspects   map[string]float32 // width over height, by episode
	autoNext  bool
	epRange   int // which block of 50 episodes the list shows
}

func newApp() *app {
	st := loadSettings()
	a := &app{
		page:         "home",
		backTo:       "home",
		feeds:        map[string]*feed{},
		aspects:      map[string]float32{},
		scrolls:      map[string]*ui.ScrollState{},
		lib:          newLibrary(),
		theme:        netflixTheme(),
		history:      loadHistory(),
		myList:       loadMyList(),
		episodeCache: map[string]*hongguo.Detail{},
		autoNext:     st.AutoNext,
		saved:        st,
		videoCache:   st.VideoCache,
		current:      -1,
		heroAt:       time.Now(),
	}
	a.lib.limit.Store(videoCacheSizes[st.VideoCache])
	go a.lib.prune()
	return a
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
				if page <= 1 && len(f.items) > 0 {
					// Keeps what showed, from the disk or before, as its first
					// page, and tries again in a minute.
					f.page = max(f.page, 1)
					f.loadedAt = time.Now().Add(time.Minute - catalogTTL)
					return
				}
				f.err = err.Error()
				return
			}
			if page <= 1 {
				f.loadedAt = time.Now()
				if key != "search" {
					saveCatalog(key, p)
				}
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
// A category shows at once what the disk kept of it, while it loads.
func (a *app) ensureFeed(key string) *feed {
	f := a.feed(key)
	if f.page == 0 && !f.loading && f.err == "" {
		if key != "search" && len(f.items) == 0 {
			if p, ok := loadCatalog(key); ok {
				f.items, f.page, f.totalPages = p.Items, 1, p.TotalPages
				f.page = 0 // still loads it afresh below
			}
		}
		a.loadFeed(key, 1)
	}
	return f
}

// refreshFeeds loads again the first page of the categories that loaded
// long ago and showed no more of it, as the catalog changes.
func (a *app) refreshFeeds() {
	for _, cat := range hongguo.Categories {
		f := a.feed(cat.Slug)
		if f.page == 1 && !f.loading && time.Since(f.loadedAt) > catalogTTL {
			a.loadFeed(cat.Slug, 1)
		}
	}
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
	if a.page == "watch" && page != "watch" {
		a.tellPlayer("smovie.pause", nil) // as streaming apps do on leaving the player
	}
	if page != "watch" {
		a.backTo = page
	}
	if page == "settings" && a.page != "settings" {
		a.measureUsage()
	}
	a.page = page
}

// watchOf returns the history of the series, if it was played.
func (a *app) watchOf(id string) (watched, bool) {
	for _, w := range a.history {
		if w.Series.ID == id {
			return w, true
		}
	}
	return watched{}, false
}

// lastEpisode is the episode index a series picks up at: where it was
// left, or the first again once it was watched to its end.
func (a *app) lastEpisode(id string) (int, bool) {
	w, ok := a.watchOf(id)
	if !ok || w.Finished {
		return 0, ok
	}
	return w.Episode, true
}

// --- playback ---

// open shows a series and plays it from where the user left it.
func (a *app) open(s hongguo.Series) { a.openAt(s, -1) }

// openAt shows a series and plays its episode index ep, or, for -1,
// where the user left it.
func (a *app) openAt(s hongguo.Series, ep int) {
	a.detailsOpen = false
	if a.page != "watch" {
		a.backTo = a.page
	}
	a.page = "watch"
	if a.series != nil && a.series.ID == s.ID {
		if ep >= 0 && a.detail != nil && ep != a.current {
			a.play(min(ep, len(a.detail.VIDs)-1))
		}
		return
	}
	start := ep
	if start < 0 {
		start, _ = a.lastEpisode(s.ID)
	}
	a.series = &s
	a.detail, a.detailErr = nil, ""
	a.current, a.job = -1, nil
	a.epRange = start / epBlock
	a.tellPlayer("smovie.pause", nil)
	a.scroll("watch").Y = 0
	a.detailSeq++
	seq := a.detailSeq
	a.episodes(s, func(d *hongguo.Detail, err error) {
		if seq != a.detailSeq {
			return
		}
		if err != nil {
			a.detailErr = err.Error()
			return
		}
		a.detail = d
		a.play(min(start, len(d.VIDs)-1))
	})
}

// episodes loads the episodes of a series, once, and gives them to done
// on the main thread.
func (a *app) episodes(s hongguo.Series, done func(*hongguo.Detail, error)) {
	if d, ok := a.episodeCache[s.ID]; ok {
		done(d, nil)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		d, err := hongguo.Episodes(ctx, s.ID)
		a.win.Update(func() {
			if err == nil {
				if d.Intro == "" {
					d.Intro = s.Intro
				}
				if len(d.Tags) == 0 {
					d.Tags = s.Tags
				}
				if d.Cover == "" {
					d.Cover = s.Cover
				}
				a.episodeCache[s.ID] = d
			}
			done(d, err)
		})
	}()
}

func (a *app) play(index int) {
	if a.detail == nil || index < 0 || index >= len(a.detail.VIDs) {
		return
	}
	a.current = index
	a.epRange = index / epBlock
	vid := a.detail.VIDs[index]
	total := len(a.detail.VIDs)
	var start float64
	if w, ok := a.watchOf(a.detail.ID); ok && !w.Finished {
		start = w.resumeAt(index)
	}
	a.history = recordWatch(a.history, a.detail.Series, index, total)
	saveHistory(append([]watched(nil), a.history...))
	j := a.lib.ensure(vid)
	a.job = j
	info := episodeInfo{VID: vid, Ep: index + 1, Total: total, Title: a.detail.Title, Poster: a.detail.Cover, Start: start}
	a.lib.setPlaying(vid)
	a.server.setCurrent(info)
	a.tellPlayer("smovie.load", info)
	a.playSeq++
	seq := a.playSeq
	go func() {
		<-j.done
		a.win.Update(func() {
			if seq != a.playSeq || j.err != nil {
				return
			}
			// Prefetch the next episode so continuous play does not wait.
			if next := index + 1; a.detail != nil && next < len(a.detail.VIDs) {
				a.lib.ensure(a.detail.VIDs[next])
			}
		})
	}()
}

// tellPlayer calls a function of the player page with a JSON argument.
// A page still loading misses it, and reads the episode from /current.
func (a *app) tellPlayer(fn string, arg any) {
	data, _ := json.Marshal(arg)
	page := a.player.Page()
	go func() { _, _ = page.Eval(fmt.Sprintf("window.smovie && %s(%s)", fn, data)) }()
}

// step plays the episode before (-1) or after (+1) vid, when vid still
// plays: from the player's buttons and when an episode ends.
func (a *app) step(vid string, delta int, ended bool) {
	a.win.Update(func() {
		if !a.isCurrent(vid) || (ended && !a.autoNext) {
			return
		}
		a.play(a.current + delta)
	})
}

func (a *app) retry(vid string) {
	a.win.Update(func() {
		if a.isCurrent(vid) {
			a.play(a.current)
		}
	})
}

func (a *app) progress(vid string, pos, dur float64, ended bool) {
	a.win.Update(func() {
		if a.isCurrent(vid) && recordProgress(a.history, a.detail.ID, a.current, pos, dur, ended) {
			saveHistory(append([]watched(nil), a.history...))
		}
	})
}

// forget takes a series out of the history, offering to put it back.
func (a *app) forget(c *ui.Context, id string) {
	list, w, at := removeWatch(a.history, id)
	if at < 0 {
		return
	}
	a.history = list
	saveHistory(append([]watched(nil), a.history...))
	c.ToastAction("已从观看记录中移除《"+w.Series.Title+"》", "撤销", func() {
		a.history = restoreWatch(a.history, w, at)
		saveHistory(append([]watched(nil), a.history...))
	})
}

// forgetAll empties the history.
func (a *app) forgetAll() {
	a.history = nil
	saveHistory(nil)
}

func (a *app) meta(vid string, w, h int) {
	a.win.Update(func() { a.aspects[vid] = float32(w) / float32(h) })
}

func (a *app) isCurrent(vid string) bool {
	return a.detail != nil && a.current >= 0 && a.current < len(a.detail.VIDs) && a.detail.VIDs[a.current] == vid
}

// aspect is the width over the height of the episode that plays, 16:9
// until known.
func (a *app) aspect() float32 {
	if a.detail == nil || a.current < 0 || a.current >= len(a.detail.VIDs) {
		return 16.0 / 9
	}
	vid := a.detail.VIDs[a.current]
	if r, ok := a.aspects[vid]; ok {
		return r
	}
	if w, h := a.lib.size(vid); w > 0 && h > 0 {
		return float32(w) / float32(h)
	}
	// Episodes of a series share a shape: use another's.
	for _, v := range a.detail.VIDs {
		if r, ok := a.aspects[v]; ok {
			return r
		}
	}
	return 16.0 / 9
}

// onReady, when set, runs once the window and the player exist.
var onReady func(*app)

func main() {
	a := newApp()
	srv, err := startMediaServer(a.lib, playerEvents{step: a.step, retry: a.retry, meta: a.meta, progress: a.progress})
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
		win := a.win
		a.images = newImageCache(win.Invalidate)
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
		_ = a.player.Page().LoadURL(a.server.playerURL())
		for _, cat := range hongguo.Categories {
			a.ensureFeed(cat.Slug)
		}
		if onReady != nil {
			onReady(a)
		}
		a.win.Invalidate()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
