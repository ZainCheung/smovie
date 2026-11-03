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
