package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

func testHistory() []watched {
	now := time.Now()
	return []watched{
		{Series: hongguo.Series{ID: "1000001", Title: "第一部剧"}, Episode: 2, Total: 10, Position: 30, Duration: 60, Updated: now},
		{Series: hongguo.Series{ID: "1000002", Title: "第二部剧"}, Episode: 9, Total: 10, Finished: true, Updated: now.Add(-time.Hour)},
	}
}

func TestRecordWatchKeepsPosition(t *testing.T) {
	list := testHistory()
	s := list[0].Series
	same := recordWatch(list, s, 2, 10)
	if same[0].Position != 30 || same[0].resumeAt(2) != 30 {
		t.Fatalf("same episode lost its position: %+v", same[0])
	}
	next := recordWatch(list, s, 3, 10)
	if next[0].Position != 0 || next[0].resumeAt(3) != 0 {
		t.Fatalf("next episode kept a position: %+v", next[0])
	}
	if len(next) != 2 {
		t.Fatalf("len %d", len(next))
	}
}

func TestRecordProgressFinishes(t *testing.T) {
	list := testHistory()
	list[0].Episode = 9
	if !recordProgress(list, "1000001", 9, 59, 60, true) || !list[0].Finished || list[0].progress() != 1 {
		t.Fatalf("not finished: %+v", list[0])
	}
	if recordProgress(list, "1000001", 3, 1, 60, false) {
		t.Fatal("progress of another episode applied")
	}
}

func TestRemoveRestore(t *testing.T) {
	list := testHistory()
	out, w, at := removeWatch(list, "1000001")
	if len(out) != 1 || at != 0 || len(list) != 2 {
		t.Fatalf("remove: %d %d", len(out), at)
	}
	back := restoreWatch(out, w, at)
	if len(back) != 2 || back[0].Series.ID != "1000001" {
		t.Fatalf("restore: %+v", back)
	}
}

// The history page's delete button removes the entry without opening it,
// and clearing all asks first.
func TestHistoryPageDelete(t *testing.T) {
	historyFile = filepath.Join(t.TempDir(), "history.json")
	a := newApp()
	a.history = testHistory()
	a.page = "history"
	var tt *ui.Tester
	a.images = newImageCache(func() {})
	tt = ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme)
		ui.Scroll(c).Fill().Children(func() { a.historyPage(c) })
	}, 1200, 800)
	tt.Frame()
	if !tt.HasText("第一部剧") || !tt.HasText("已看完") {
		t.Fatalf("texts: %v", tt.Texts())
	}
	r, ok := tt.Find("删除")
	if !ok {
		t.Fatal("no delete button")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2) // the row shows its buttons under the pointer
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.page != "history" {
		t.Fatalf("delete opened the series: page %q", a.page)
	}
	if len(a.history) != 1 || a.history[0].Series.ID != "1000002" {
		t.Fatalf("history after delete: %+v", a.history)
	}
	if err := tt.Click("清空全部"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.confirmClear || len(a.history) != 1 {
		t.Fatal("clearing did not ask first")
	}
	if err := tt.Click("清空"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if len(a.history) != 0 || !tt.HasText("还没有观看记录") {
		t.Fatalf("not cleared: %d", len(a.history))
	}
}
