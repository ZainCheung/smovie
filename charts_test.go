package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// A category narrows to the genre chosen, from the chips its first page
// lists, and back to all of it.
func TestGenreChips(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	a.images = newImageCache(func() {})
	f := a.feed("real-drama")
	f.page, f.items = 1, []hongguo.Series{{ID: "4000001", Title: "全部的剧"}}
	f.genres = []hongguo.Genre{{Slug: "clan", Label: "家族"}, {Slug: "romance", Label: "爱情"}}
	clan := a.feed("real-drama/clan")
	clan.page, clan.items = 1, []hongguo.Series{{ID: "4000002", Title: "家族的剧"}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Scroll(c).Fill().Children(func() { a.categoryPage(c, "real-drama") })
	}, 1280, 900)
	tt.Frame()
	if !tt.HasText("全部真人剧") || !tt.HasText("全部的剧") {
		t.Fatalf("texts %v", tt.Texts())
	}
	if err := tt.Click("家族"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if key, _ := a.categoryFeed("real-drama"); key != "real-drama/clan" || !tt.HasText("真人剧 · 家族") || !tt.HasText("家族的剧") {
		t.Fatalf("key %s, texts %v", key, tt.Texts())
	}
	if err := tt.Click("全部"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.genre["real-drama"] != "" || !tt.HasText("全部真人剧") {
		t.Fatalf("genre %q", a.genre["real-drama"])
	}
}
