package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

func TestFollowedAlbumInMyList(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	a.history, a.myList = nil, nil
	a.images = newImageCache(func() {})
	seasons := []hongguo.Season{
		{Series: hongguo.Series{ID: "3000001", Title: "合集剧"}, Index: 1},
		{Series: hongguo.Series{ID: "3000002", Title: "合集剧第二季"}, Index: 2},
	}
	a.albums = []followed{{AlbumID: "9000001", Seasons: seasons, Seen: 1, Added: time.Now()}}
	// Known already: no load from the network.
	a.seasons["9000001"] = &loaded[[]hongguo.Season]{value: &seasons}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Scroll(c).Fill().Children(func() { a.myListPage(c) })
	}, 1280, 900)
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"订阅的合集", "合集剧", "新季", "第 2 季上新 · 共 2 季"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %v", want, tt.Texts())
		}
	}
}

// The seasons found show from the disk at once, on the next launch.
func TestSeasonCache(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	v := []hongguo.Season{{Series: hongguo.Series{ID: "3000001", Title: "合集剧"}, Index: 1}, {Series: hongguo.Series{ID: "3000002", Title: "合集剧第二季"}, Index: 2}}
	a.cacheSeasons("9000001", v)
	time.Sleep(100 * time.Millisecond)
	b := newApp()
	if got := b.seasonCache()["9000001"]; len(got) != 2 || got[1].ID != "3000002" {
		t.Fatalf("cached %+v", got)
	}
}

func TestSeasonCacheDropsUnreleasedSeasons(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	v := []hongguo.Season{
		{Series: hongguo.Series{ID: "7710256822552103998", Title: "符仙人第十七季", EpisodeCount: 290, EpisodeLabel: "全290集"}, Index: 17},
		{Series: hongguo.Series{ID: "7711670675403983897", Title: "符仙人第十八季", EpisodeCount: 0, EpisodeLabel: ""}, Index: 18},
	}
	a.cacheSeasons("7667228805131406360", v)
	saveAlbums([]followed{{AlbumID: "7667228805131406360", Seasons: v, Seen: 2, Added: time.Now()}})
	time.Sleep(100 * time.Millisecond)

	b := newApp()
	if got := b.seasonCache()["7667228805131406360"]; len(got) != 1 || got[0].ID != "7710256822552103998" {
		t.Fatalf("expected cached unreleased season to be pruned, got %+v", got)
	}
	if len(b.albums) != 1 || len(b.albums[0].Seasons) != 1 || b.albums[0].Seen != 1 {
		t.Fatalf("expected followed album unreleased season to be pruned and Seen clamped, got %+v", b.albums)
	}
}

func TestFollowedAlbumMarksAllSeasonsInMyList(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	a.history, a.myList = nil, nil
	a.images = newImageCache(func() {})
	s1 := hongguo.Series{ID: "7710256822552103998", Title: "符仙人第十七季", EpisodeCount: 290, EpisodeLabel: "全290集"}
	s2 := hongguo.Series{ID: "7708700298868755480", Title: "符仙人第十六季", EpisodeCount: 270, EpisodeLabel: "全270集"}
	s3 := hongguo.Series{ID: "7711670675403983897", Title: "符仙人第十八季"}
	seasons := []hongguo.Season{{Series: s2, Index: 16}, {Series: s1, Index: 17}}
	a.albums = []followed{{AlbumID: "7667228805131406360", Seasons: seasons, Seen: 2, Added: time.Now()}}
	// Even a season matched via detail.AlbumID is recognized as part of the followed collection.
	a.detail = &hongguo.Detail{Series: s3, AlbumID: "7667228805131406360"}

	if !a.inMyList(s1.ID) || !a.inMyList(s2.ID) || !a.inMyList(s3.ID) {
		t.Fatalf("expected all seasons of followed album to be inMyList: s1=%v s2=%v s3=%v",
			a.inMyList(s1.ID), a.inMyList(s2.ID), a.inMyList(s3.ID))
	}

	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		a.listPill(c, s1)
	}, 400, 100)
	tt.Frame()
	if !tt.HasText("已在片单") {
		t.Fatalf("expected listPill to show 已在片单 for followed album season, texts=%v", tt.Texts())
	}

	// Clicking the pill unfollows the collection so state stays consistent.
	if err := tt.Click("从我的片单移除"); err != nil {
		t.Fatalf("click listPill: %v", err)
	}
	tt.Frame()
	if a.inMyList(s1.ID) || len(a.albums) != 0 {
		t.Fatalf("expected clicking 已在片单 to unfollow album, inMyList=%v albums=%d", a.inMyList(s1.ID), len(a.albums))
	}
	if !tt.HasText("加入片单") {
		t.Fatalf("expected listPill to revert to 加入片单, texts=%v", tt.Texts())
	}
}
