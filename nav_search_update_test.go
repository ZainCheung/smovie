package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

func newTestUIApp(t *testing.T) *app {
	t.Helper()
	configOverride = t.TempDir()
	a := newApp()
	a.images = newImageCache(func() {})
	a.server = &mediaServer{}
	hot := []hongguo.HotQuery{
		{Title: "重生高三"},
		{Title: "十八岁太奶奶"},
		{Title: "宴律"},
	}
	a.hotSearch = &loaded[[]hongguo.HotQuery]{value: &hot}
	pop := a.feed(rankKey(hongguo.Rankings[0].Slug))
	pop.items = []hongguo.Series{
		{ID: "7709153171259542553", Title: "重生高三，请停止你的魅力输出", EpisodeLabel: "全150集", Tags: []string{"重生", "校园"}},
		{ID: "7503745441910017049", Title: "宴律，你的白月光回国了", EpisodeLabel: "全76集", Tags: []string{"甜宠"}},
	}
	pop.page, pop.totalPages = 1, 1
	return a
}

func TestNavBarSearchButtonOpensNetflixSearchPage(t *testing.T) {
	a := newTestUIApp(t)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Box(c).Fill().Children(func() {
			if a.page == "search" {
				ui.Scroll(c.Key("search")).Fill().Children(func() { a.searchPage(c) })
			}
			a.navBar(c, 0)
		})
	}, 1280, 820)
	tt.Frame()

	// The old inline search field placeholder is gone; the search icon button is shown.
	if tt.HasText("片名、题材、演员") {
		t.Fatalf("inline search box should be replaced by icon button, texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("更新"); ok {
		t.Fatal("update button should be hidden when no update is available")
	}
	searchRect, ok := tt.Find("搜索")
	if !ok {
		t.Fatal("search icon button missing from navbar")
	}
	histRect, ok := tt.Find("观看记录")
	if !ok {
		t.Fatal("history icon button missing from navbar")
	}
	gearRect, ok := tt.Find("设置")
	if !ok {
		t.Fatal("settings icon button missing from navbar")
	}
	if !(searchRect.X < histRect.X && histRect.X < gearRect.X) {
		t.Fatalf("unexpected toolbar order: search=%+v hist=%+v gear=%+v", searchRect, histRect, gearRect)
	}

	// Clicking the search icon enters the Netflix-style search page.
	if err := tt.Click("搜索"); err != nil {
		t.Fatalf("click search button: %v", err)
	}
	tt.Frame()
	if a.page != "search" {
		t.Fatalf("expected page=search, got %q", a.page)
	}
	if !tt.HasText("大家都在搜") || !tt.HasText("热门搜索推荐") || !tt.HasText("重生高三") {
		t.Fatalf("expected Netflix discovery sections on idle search page, texts=%v", tt.Texts())
	}
	if !tt.Focused("搜索短剧") {
		t.Fatal("expected search page input to receive focus on open")
	}

	// Clicking a trending query populates search state and shows results view.
	if err := tt.Click("重生高三"); err != nil {
		t.Fatalf("click trending query: %v", err)
	}
	a.searchSuggestions = []string{"重生高三", "重生校园", "重生之逆袭"}
	f := a.feed("search")
	f.loading = false
	f.items = []hongguo.Series{{ID: "7709153171259542553", Title: "重生高三，请停止你的魅力输出", EpisodeLabel: "全150集"}}
	tt.Frame()

	if a.searched != "重生高三" || a.query != "重生高三" {
		t.Fatalf("expected query/searched to be 重生高三, got %q / %q", a.query, a.searched)
	}
	if !tt.HasText("的搜索结果") || !tt.HasText("探索相关：") || !tt.HasText("重生校园") {
		t.Fatalf("expected results heading and related suggestions, texts=%v", tt.Texts())
	}

	// Clearing resets back to the discovery screen.
	if err := tt.Click("清空搜索"); err != nil {
		t.Fatalf("click clear search: %v", err)
	}
	tt.Frame()
	if a.query != "" || a.searched != "" {
		t.Fatalf("expected cleared search state, got query=%q searched=%q", a.query, a.searched)
	}
	if !tt.HasText("热门搜索推荐") {
		t.Fatalf("expected discovery screen after clearing, texts=%v", tt.Texts())
	}
}

func TestNavUpdateButtonLifecycleAndRelaunch(t *testing.T) {
	oldEnabled, oldCheck, oldRelaunch := updaterEnabled, checkAppUpdate, relaunchApp
	t.Cleanup(func() {
		updaterEnabled, checkAppUpdate, relaunchApp = oldEnabled, oldCheck, oldRelaunch
	})

	installStarted := make(chan struct{})
	installProceed := make(chan struct{})
	var relaunched atomic.Bool

	updaterEnabled = func() bool { return true }
	checkAppUpdate = func(ctx context.Context) (*appUpdate, error) {
		return &appUpdate{
			Version: "v0.1.17",
			Install: func(ctx context.Context, progress func(downloaded, total int64)) error {
				progress(50, 100)
				close(installStarted)
				<-installProceed
				progress(100, 100)
				return nil
			},
		}, nil
	}
	relaunchApp = func() { relaunched.Store(true) }

	a := newTestUIApp(t)
	a.checkForUpdateOnLaunch()

	deadline := time.Now().Add(2 * time.Second)
	for a.pendingUpdate == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.pendingUpdate == nil || a.newVersion != "v0.1.17" {
		t.Fatalf("expected launch check to populate pendingUpdate v0.1.17, got %+v (%q)", a.pendingUpdate, a.newVersion)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Box(c).Fill().Children(func() {
			a.navBar(c, 0)
		})
	}, 1280, 160)
	tt.Frame()

	// Positioned leftmost among toolbar buttons; 32x32 icon-only button at rest.
	updRect, ok := tt.Find("更新")
	if !ok {
		t.Fatal("update button not found after update became available")
	}
	searchRect, _ := tt.Find("搜索")
	if !(updRect.X < searchRect.X) {
		t.Fatalf("update button must be leftmost before search: upd=%+v search=%+v", updRect, searchRect)
	}
	countText := func(target string) int {
		n := 0
		for _, s := range tt.Texts() {
			if s == target {
				n++
			}
		}
		return n
	}
	if updRect.W != 32 || countText("更新") != 1 {
		t.Fatalf("expected 32px icon-only button at rest (label only), got rect=%+v texts=%v", updRect, tt.Texts())
	}

	// Hover morphs the download icon button into the wider text pill "更新".
	tt.Move(updRect.X+updRect.W/2, updRect.Y+updRect.H/2)
	tt.Frame()
	hoveredRect, _ := tt.Find("更新")
	if hoveredRect.W <= 32 || countText("更新") < 2 {
		t.Fatalf("expected hovered update button to expand into text pill '更新', rect=%+v texts=%v", hoveredRect, tt.Texts())
	}

	// Clicking starts background download and turns the button into a spinner.
	tt.ClickAt(hoveredRect.X+hoveredRect.W/2, hoveredRect.Y+hoveredRect.H/2)
	tt.Frame()
	select {
	case <-installStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background update install to start")
	}
	if !a.updateDownloading {
		t.Fatal("expected updateDownloading=true while installing")
	}
	if countText("更新") != 0 {
		t.Fatalf("text '更新' should be replaced by spinner while downloading, texts=%v", tt.Texts())
	}
	spinRect, ok := tt.Find("正在下载更新")
	if !ok || spinRect.W != 32 {
		t.Fatalf("expected 32px downloading spinner button, got ok=%v rect=%+v", ok, spinRect)
	}

	// Completing installation triggers relaunch.
	close(installProceed)
	deadline = time.Now().Add(2 * time.Second)
	for !relaunched.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !relaunched.Load() {
		t.Fatal("expected app to relaunch once update installation finished")
	}
}

func TestNavUpdateButtonDownloadFailureRecovers(t *testing.T) {
	a := newTestUIApp(t)
	done := make(chan struct{})
	a.pendingUpdate = &appUpdate{
		Version: "v0.1.17",
		Install: func(ctx context.Context, progress func(downloaded, total int64)) error {
			defer close(done)
			return errors.New("network error")
		},
	}

	a.startUpdateInstall()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failed install")
	}
	deadline := time.Now().Add(2 * time.Second)
	for a.updateDownloading && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.updateDownloading {
		t.Fatal("expected updateDownloading to reset after failure")
	}
	if a.pendingToast == "" {
		t.Fatal("expected failure toast message to be queued")
	}
}
