package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// historyPage lists what was watched, newest first and by day, each with
// where it was left, to pick up or to remove, as streaming services'
// viewing activity.
func (a *app) historyPage(c *ui.Context) {
	ui.Column(c).Gap(18).Padding(navHeight+28, pagePad, 56, pagePad).Children(func() {
		ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
			ui.Text(c, "观看记录").FontSize(28).FontWeight(800)
			if n := len(a.history); n > 0 {
				ui.Textf(c, "共 %d 部", n).FontSize(13).TextColor(colorFaint)
			}
			ui.Spacer(c)
			if len(a.history) > 0 {
				clear := ui.ButtonBase(c.Key("clear-all")).Row().AlignItems(ui.Center).Gap(6).Padding(7, 14).Radius(6).Label("清空全部")
				clear.Background(rgba(255, 255, 255, 18+28*b2f(clear.Hovered())))
				clear.Children(func() {
					ui.Icon(c, iconTrash).Size(15, 15).TextColor(colorMuted)
					ui.Text(c, "清空全部").FontSize(13).TextColor(colorMuted)
				})
				if clear.Clicked() {
					a.confirmClear = true
				}
			}
		})
		if ui.AlertDialog(c, &a.confirmClear, "清空观看记录？", "所有剧集的观看进度都会被删除，且无法恢复。", "取消", "清空") == 1 {
			a.forgetAll()
		}
		if len(a.history) == 0 {
			ui.Column(c).Padding(100, 0).Center().Gap(14).Children(func() {
				ui.Icon(c, iconClock).Size(44, 44).TextColor(colorFaint)
				ui.Text(c, "还没有观看记录").FontSize(16).TextColor(colorMuted)
				if bigButton(c, "go-home", iconPlayFill, "去首页看看", true).Clicked() {
					a.go_("home")
				}
			})
			return
		}
		now := time.Now()
		day := ""
		for _, w := range a.history {
			if d := dayLabel(w.Updated, now); d != day {
				day = d
				ui.Text(c, d).FontSize(14).FontWeight(700).TextColor(colorMuted).Margin(10, 0, 0, 0)
			}
			a.historyRow(c, w, now)
		}
	})
}

func (a *app) historyRow(c *ui.Context, w watched, now time.Time) {
	id := w.Series.ID
	row := ui.Row(c.Key("h-"+id)).AlignItems(ui.Center).Gap(18).Padding(10, 12).Radius(8).
		Cursor(ui.CursorPointer).Label(w.Series.Title)
	hover := row.Hovered()
	row.Background(rgba(255, 255, 255, 14*b2f(hover)))
	if row.Clicked() {
		a.open(w.Series)
	}
	row.Children(func() {
		ui.Box(c).Size(66, 88).Shrink(0).Radius(5).Clip().Background(colorCard).Children(func() {
			pic := a.images.Get(c, w.Series.Cover, imgSmall, row.InView())
			if pic.Img == nil || pic.Fade < 1 {
				posterFallback(c, "", pic.Failed)
			}
			if pic.Img != nil {
				ui.Image(c, pic.Img).Absolute().Left(0).Top(0).Right(0).Bottom(0).Fit(ui.Cover).Opacity(pic.Fade)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, w.Series.Title).FontSize(16).FontWeight(700).MaxLines(1)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, watchNote(w)).FontSize(13).TextColor(colorMuted)
				ui.Box(c).Size(3, 3).Radius(2).Background(colorFaint)
				ui.Text(c, timeLabel(w.Updated, now)).FontSize(13).TextColor(colorFaint)
			})
			ui.Box(c).Width(260).Height(4).Radius(2).Background(rgba(255, 255, 255, 40)).Margin(4, 0, 0, 0).Children(func() {
				ui.Box(c).Width(260 * w.progress()).Height(4).Radius(2).Background(colorRed)
			})
		})
		label := "继续播放"
		if w.Finished {
			label = "重新播放"
		}
		play := ui.ButtonBase(c.Key("play")).Row().AlignItems(ui.Center).Gap(6).Padding(7, 16, 7, 12).Radius(6).Label(label)
		play.Opacity(0.35 + 0.65*b2f(hover || play.FocusVisible()))
		play.Background(map[bool]ui.Color{true: colorWhite, false: rgba(255, 255, 255, 220)}[play.Hovered()])
		play.Children(func() {
			ui.Icon(c, iconPlayFill).Size(14, 14).TextColor(rgba(0, 0, 0, 255))
			ui.Text(c, label).FontSize(13).FontWeight(700).TextColor(rgba(0, 0, 0, 255))
		})
		if play.Clicked() {
			a.open(w.Series)
		}
		del := ui.ButtonBase(c.Key("del")).Size(34, 34).Radius(17).Center().Label("删除").Tooltip("从观看记录中删除")
		del.Background(rgba(255, 255, 255, 18+40*b2f(del.Hovered())))
		del.Children(func() {
			ui.Icon(c, iconClose).Size(16, 16).TextColor(map[bool]ui.Color{true: colorWhite, false: colorMuted}[del.Hovered()])
		})
		if del.Clicked() {
			a.forget(c, id)
		}
	})
}

// dayLabel groups the history: 今天, 昨天, or the date.
func dayLabel(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "今天"
	case t.YearDay() == now.AddDate(0, 0, -1).YearDay() && now.Sub(t) < 48*time.Hour:
		return "昨天"
	case y1 == y2:
		return fmt.Sprintf("%d 月 %d 日", m1, d1)
	}
	return fmt.Sprintf("%d 年 %d 月 %d 日", y1, m1, d1)
}

// timeLabel is when an entry was last watched, as "3 分钟前" or "15:04".
func timeLabel(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	}
	return t.Format("15:04")
}
