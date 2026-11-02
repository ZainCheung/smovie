package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// listed is a series the user keeps in My List.
type listed struct {
	Series hongguo.Series `json:"series"`
	Added  time.Time      `json:"added"`
}

func myListPath() string { return filepath.Join(configDir(), "mylist.json") }

func loadMyList() []listed {
	data, err := os.ReadFile(myListPath())
	if err != nil {
		return nil
	}
	var list []listed
	_ = json.Unmarshal(data, &list)
	return list
}

var (
	myListMu  sync.Mutex
	myListGen int
)

// saveMyList writes the list on another goroutine; call on the main thread
// with a copy the caller no longer changes.
func saveMyList(list []listed) {
	myListMu.Lock()
	myListGen++
	gen := myListGen
	myListMu.Unlock()
	go func() {
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return
		}
		myListMu.Lock()
		defer myListMu.Unlock()
		if gen != myListGen {
			return
		}
		p := myListPath()
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if os.WriteFile(p+".tmp", data, 0o644) == nil {
			_ = os.Rename(p+".tmp", p)
		}
	}()
}

func (a *app) inMyList(id string) bool {
	for _, l := range a.myList {
		if l.Series.ID == id {
			return true
		}
	}
	return false
}

// toggleMyList adds the series to the front of My List, or takes it out.
func (a *app) toggleMyList(c *ui.Context, s hongguo.Series) {
	if a.inMyList(s.ID) {
		var out []listed
		for _, l := range a.myList {
			if l.Series.ID != s.ID {
				out = append(out, l)
			}
		}
		a.myList = out
		c.Toast("已从我的片单移除")
	} else {
		a.myList = append([]listed{{Series: s, Added: time.Now()}}, a.myList...)
		c.Toast("已加入我的片单")
	}
	saveMyList(append([]listed(nil), a.myList...))
}

func (a *app) myListSeries() []hongguo.Series {
	out := make([]hongguo.Series, len(a.myList))
	for i, l := range a.myList {
		out[i] = l.Series
	}
	return out
}

// myListButton is the round ＋ / ✓ button adding a series to My List.
func (a *app) myListButton(c *ui.Context, key string, s hongguo.Series, size float32) {
	in := a.inMyList(s.ID)
	label := map[bool]string{true: "从我的片单移除", false: "加入我的片单"}[in]
	b := ui.ButtonBase(c.Key(key)).Size(size, size).Radius(size / 2).Center().Label(label).Tooltip(label)
	b.Background(rgba(42, 42, 42, 200+40*b2f(b.Hovered()))).Border(2, withAlpha(colorWhite, uint8(150+100*b2f(b.Hovered()))))
	ico := iconPlus
	if in {
		ico = iconCheck
	}
	b.Children(func() { ui.Icon(c, ico).Size(size*0.5, size*0.5).TextColor(colorWhite) })
	if b.Clicked() {
		a.toggleMyList(c, s)
	}
}

// myListPage shows My List as a grid.
func (a *app) myListPage(c *ui.Context) {
	ui.Column(c).Gap(18).Padding(navHeight+28, pagePad, 56, pagePad).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "我的片单").FontSize(28).FontWeight(800)
			if n := len(a.myList); n > 0 {
				ui.Textf(c, "共 %d 部", n).FontSize(13).TextColor(colorFaint)
			}
		})
		if len(a.myList) == 0 {
			ui.Column(c).Padding(100, 0).Center().Gap(14).Children(func() {
				ui.Icon(c, iconPlus).Size(44, 44).TextColor(colorFaint)
				ui.Text(c, "片单还是空的").FontSize(16).TextColor(colorMuted)
				ui.Text(c, "在剧集详情里点 ＋ 收藏想看的剧，之后在这里找到它们").FontSize(13).TextColor(colorFaint)
			})
			return
		}
		a.seriesGrid(c, a.myListSeries())
	})
}
