package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// The seasons of a series, as the official app's 系列剧 tab lists them,
// and the collections the user follows: My List shows them with their
// seasons, and tells when a new one comes out.

// seasonsOf returns the seasons of album, of which seed is one, starting
// to load them.
func (a *app) seasonsOf(album string, seed hongguo.Series) *loaded[[]hongguo.Season] {
	l, ok := a.seasons[album]
	if !ok {
		l = &loaded[[]hongguo.Season]{}
		a.seasons[album] = l
		a.loadSeasons(album, seed, l)
	}
	return l
}

func (a *app) loadSeasons(album string, seed hongguo.Series, l *loaded[[]hongguo.Season]) {
	l.loading, l.err = true, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		v, err := hongguo.Seasons(ctx, seed, album)
		a.win.Update(func() {
			l.loading, l.err = false, err
			if err == nil {
				l.value = &v
				a.seasonsLoaded(album, v)
			}
		})
	}()
}

// albumOf is the collection the series that plays is a season of, with
// its seasons once known; none while there is one season only.
func (a *app) albumOf() (string, *loaded[[]hongguo.Season]) {
	if a.detail == nil || a.detail.AlbumID == "" {
		return "", nil
	}
	l := a.seasonsOf(a.detail.AlbumID, a.detail.Series)
	if l.value != nil && len(*l.value) < 2 {
		return "", nil
	}
	return a.detail.AlbumID, l
}

// seasonsPanel lists the seasons in the episode panel's 系列剧 tab.
func (a *app) seasonsPanel(c *ui.Context, l *loaded[[]hongguo.Season], width float32) {
	ui.Column(c).Padding(10).Gap(6).Children(func() {
		switch {
		case l.value != nil:
		case l.loading:
			ui.Row(c).Padding(16).Justify(ui.Center).Children(func() { ui.Spinner(c).FontSize(18) })
			return
		default:
			ui.Row(c).Padding(6).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "系列剧加载失败").FontSize(13).TextColor(colorMuted)
				if ui.Button(c.Key("seasons-retry"), "重试").Clicked() && a.detail != nil {
					a.loadSeasons(a.detail.AlbumID, a.detail.Series, l)
				}
			})
			return
		}
		for _, s := range *l.value {
			a.seasonRow(c, s, width-20)
		}
	})
}

// seasonRow is a season: its poster, title and count, and a button
// playing it, or a mark on the season that plays.
func (a *app) seasonRow(c *ui.Context, s hongguo.Season, width float32) {
	cur := a.detail != nil && a.detail.ID == s.ID
	row := ui.ButtonBase(c.Key("season" + s.ID)).Width(width).Padding(8).Radius(10).Gap(12).AlignItems(ui.Center).
		Label(fmt.Sprintf("第%d季 %s", s.Index, s.Title))
	hover := row.Hovered()
	switch {
	case cur:
		row.Background(rgba(255, 255, 255, 22)).Border(1, withAlpha(colorRed, 160))
	case hover:
		row.Background(rgba(255, 255, 255, 22))
	default:
		row.Background(rgba(255, 255, 255, 10))
	}
	row.Children(func() {
		ui.Box(c).Size(48, 64).Shrink(0).Radius(6).Clip().Background(colorCard).Children(func() {
			pic := a.images.Get(c, s.Cover, imgSmall, row.InView())
			if pic.Img == nil || pic.Fade < 1 {
				posterFallback(c, s.Title, pic.Failed)
			}
			if pic.Img != nil {
				ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, s.Title).FontSize(14).FontWeight(600).SingleLine()
			sub := fmt.Sprintf("第%d季", s.Index)
			switch {
			case cur:
			case s.Heat != "":
				sub += " · " + s.Heat
			case s.EpisodeLabel != "":
				sub += " · " + s.EpisodeLabel
			}
			if w, ok := a.watchOf(s.ID); ok && !cur {
				sub += " · " + watchNote(w)
			}
			ui.Text(c, sub).FontSize(12).TextColor(colorMuted).SingleLine()
		})
		if cur {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
				equalizer(c)
				ui.Text(c, "播放中").FontSize(12).FontWeight(600).TextColor(colorRed)
			})
			return
		}
		label := "播放"
		if w, ok := a.watchOf(s.ID); ok && !w.Finished {
			label = "继续"
		}
		ui.Text(c, label).Shrink(0).FontSize(13).FontWeight(600).Padding(7, 16).Radius(8).
			Background(rgba(255, 255, 255, 26+30*b2f(hover)))
	})
	if row.Clicked() && !cur {
		a.open(s.Series)
	}
}

// --- following a collection ---

// followed is a collection the user follows in My List.
type followed struct {
	AlbumID string           `json:"albumId"`
	Seasons []hongguo.Season `json:"seasons"` // as last found
	// Seen is how many seasons the user knows of: more were found since.
	Seen  int       `json:"seen"`
	Added time.Time `json:"added"`
}

// title is the collection's: its first season's.
func (f followed) title() string {
	if len(f.Seasons) == 0 {
		return ""
	}
	return f.Seasons[0].Title
}

// latest is its last season out.
func (f followed) latest() hongguo.Season {
	if len(f.Seasons) == 0 {
		return hongguo.Season{}
	}
	return f.Seasons[len(f.Seasons)-1]
}

func albumsPath() string { return filepath.Join(configDir(), "albums.json") }

func loadAlbums() []followed {
	data, err := os.ReadFile(albumsPath())
	if err != nil {
		return nil
	}
	var list []followed
	_ = json.Unmarshal(data, &list)
	return list
}

var (
	albumsMu  sync.Mutex
	albumsGen int
)

// saveAlbums writes the collections on another goroutine; call on the main
// thread with a copy the caller no longer changes.
func saveAlbums(list []followed) {
	albumsMu.Lock()
	albumsGen++
	gen := albumsGen
	albumsMu.Unlock()
	go func() {
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return
		}
		albumsMu.Lock()
		defer albumsMu.Unlock()
		if gen != albumsGen {
			return
		}
		p := albumsPath()
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if os.WriteFile(p+".tmp", data, 0o644) == nil {
			_ = os.Rename(p+".tmp", p)
		}
	}()
}

func (a *app) followIndex(album string) int {
	for i, f := range a.albums {
		if f.AlbumID == album {
			return i
		}
	}
	return -1
}

func (a *app) saveFollowed() { saveAlbums(append([]followed(nil), a.albums...)) }

// toggleFollow follows the collection of these seasons, or stops.
func (a *app) toggleFollow(c *ui.Context, album string, seasons []hongguo.Season) {
	if i := a.followIndex(album); i >= 0 {
		a.albums = append(a.albums[:i:i], a.albums[i+1:]...)
		c.Toast("已取消订阅合集")
	} else {
		a.albums = append([]followed{{AlbumID: album, Seasons: seasons, Seen: len(seasons), Added: time.Now()}}, a.albums...)
		c.Toast("已订阅合集，有新季时会在我的片单提醒")
	}
	a.saveFollowed()
}

// seasonsLoaded keeps a followed collection's seasons up to date.
func (a *app) seasonsLoaded(album string, seasons []hongguo.Season) {
	i := a.followIndex(album)
	if i < 0 || len(seasons) < len(a.albums[i].Seasons) {
		return // a search that missed some does not take them away
	}
	a.albums[i].Seasons = seasons
	a.saveFollowed()
}

// refreshAlbums looks for new seasons of the collections followed, once a
// launch, as My List shows them.
func (a *app) refreshAlbums() {
	for _, f := range a.albums {
		if _, ok := a.seasons[f.AlbumID]; ok || len(f.Seasons) == 0 {
			continue
		}
		a.seasonsOf(f.AlbumID, f.latest().Series)
	}
}

// openAlbum plays a followed collection: its new season, when one came
// out since the user saw it, else the season they watched last, else
// the first.
func (a *app) openAlbum(i int) {
	f := a.albums[i]
	if len(f.Seasons) == 0 {
		return
	}
	target := f.Seasons[0].Series
	if len(f.Seasons) > f.Seen {
		target = f.latest().Series
	} else {
		for _, w := range a.history { // newest first
			if hasSeason(f.Seasons, w.Series.ID) {
				target = w.Series
				break
			}
		}
	}
	a.albums[i].Seen = len(f.Seasons)
	a.saveFollowed()
	a.open(target)
}

func hasSeason(list []hongguo.Season, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

// followPill follows the collection that plays, in the panel's header.
func (a *app) followPill(c *ui.Context, album string, seasons []hongguo.Season) {
	in := a.followIndex(album) >= 0
	label := map[bool]string{true: "已订阅", false: "订阅合集"}[in]
	b := ui.ButtonBase(c.Key("follow")).Height(28).Padding(0, 12, 0, 8).Radius(14).Gap(4).AlignItems(ui.Center).
		Label(map[bool]string{true: "取消订阅合集", false: "订阅合集"}[in])
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
		ui.Icon(c, ico).Size(15, 15).TextColor(fg)
		ui.Text(c, label).FontSize(12).FontWeight(700).TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		a.toggleFollow(c, album, seasons)
	}
}

// albumRow shows the collections followed in My List: each as its latest
// season's poster, with how many seasons, and a mark when a new one came.
func (a *app) albumRow(c *ui.Context, cw float32) {
	if len(a.albums) == 0 {
		return
	}
	a.refreshAlbums()
	ui.Column(c.Key("albums")).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			ui.Text(c, "订阅的合集").FontSize(18).FontWeight(700)
			ui.Textf(c, "%d 个", len(a.albums)).FontSize(12).TextColor(colorFaint)
		})
		ui.Row(c).Wrap().GapX(14).GapY(22).Padding(8, 0).Children(func() {
			for i, f := range a.albums {
				s := f.latest().Series
				note := fmt.Sprintf("共 %d 季", len(f.Seasons))
				fresh := len(f.Seasons) > f.Seen
				if fresh {
					note = fmt.Sprintf("第 %d 季上新 · 共 %d 季", f.latest().Index, len(f.Seasons))
				}
				s.Title = f.title()
				s.EpisodeLabel = ""
				ui.Box(c).Key("album" + f.AlbumID).Children(func() {
					a.posterCard(c, s, cw, note, &cardExtra{
						open:        func() { a.openAlbum(i) },
						remove:      func() { a.albums = append(a.albums[:i:i], a.albums[i+1:]...); a.saveFollowed() },
						removeLabel: "取消订阅合集",
					})
					if fresh {
						ui.Text(c, "新季").Absolute().Left(8).Top(8).FontSize(11).FontWeight(800).Padding(3, 8).Radius(4).
							Background(colorRed).TextColor(colorWhite)
					}
				})
			}
		})
	})
}
