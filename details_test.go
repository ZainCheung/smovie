package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

func TestDetailsDialogAndMyList(t *testing.T) {
	configOverride = t.TempDir()
	a := newApp()
	a.history = nil
	a.myList = nil
	a.images = newImageCache(func() {})
	s := hongguo.Series{ID: "2000001", Title: "测试剧", Tags: []string{"都市", "逆袭"}, Intro: "简介"}
	other := hongguo.Series{ID: "2000002", Title: "相似剧", Tags: []string{"逆袭"}}
	unrelated := hongguo.Series{ID: "2000003", Title: "无关剧", Tags: []string{"古装"}}
	a.feed("real-drama").items = []hongguo.Series{s, other, unrelated}
	a.episodeCache[s.ID] = &hongguo.Detail{Series: s, VIDs: []string{"1", "2", "3"}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Box(c).Fill().Children(func() {
			ui.Scroll(c).Fill().Children(func() { a.myListPage(c) })
			a.detailsDialog(c)
		})
	}, 1280, 900)
	tt.Frame()
	if !tt.HasText("片单还是空的") {
		t.Fatal("no empty state")
	}
	a.showDetails(s)
	tt.Frame()
	for _, want := range []string{"测试剧", "选集", "共 3 集", "更多类似", "相似剧"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("无关剧") {
		t.Fatal("an unrelated series shows as similar")
	}
	if err := tt.Click("加入我的片单"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.inMyList(s.ID) {
		t.Fatal("not added")
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if a.detailsOpen || !tt.HasText("共 1 部") {
		t.Fatalf("open %v, texts %v", a.detailsOpen, tt.Texts())
	}
	// It persists.
	time.Sleep(100 * time.Millisecond)
	if l := loadMyList(); len(l) != 1 || l[0].Series.ID != s.ID {
		t.Fatalf("saved %+v", l)
	}
}
