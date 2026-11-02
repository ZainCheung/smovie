package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// settingsPage holds playback choices and what the caches take on disk.
func (a *app) settingsPage(c *ui.Context) {
	ui.Column(c).Gap(26).Padding(navHeight+28, pagePad, 56, pagePad).MaxWidth(860).Children(func() {
		ui.Text(c, "设置").FontSize(28).FontWeight(800)

		section(c, "播放", func() {
			settingRow(c, "自动连播", "一集播完后自动播放下一集", func() {
				ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
			})
		})

		section(c, "存储", func() {
			settingRow(c, "视频缓存上限", "超出后自动清理最久未看的剧集，正在播放的不受影响", func() {
				ui.Segmented(c.Key("cache-limit"), &a.videoCache, videoCacheLabels...).Label("视频缓存上限")
			})
			settingRow(c, "视频缓存", a.usageText(a.usage.videos), func() {
				if ui.Button(c.Key("clear-videos"), "清除").Clicked() {
					lib := a.lib
					go func() {
						lib.clear()
						a.win.Update(func() { a.measureUsage() })
					}()
					c.Toast("已清除视频缓存")
				}
			})
			settingRow(c, "封面图片缓存", a.usageText(a.usage.images), func() {
				if ui.Button(c.Key("clear-images"), "清除").Clicked() {
					images := a.images
					go func() {
						images.clearDisk()
						a.win.Update(func() { a.measureUsage() })
					}()
					c.Toast("已清除图片缓存")
				}
			})
		})

		section(c, "观看记录", func() {
			settingRow(c, "观看记录", fmt.Sprintf("共 %d 部", len(a.history)), func() {
				if ui.Button(c.Key("manage-history"), "管理").Clicked() {
					a.go_("history")
					a.scroll("history").Y = 0
				}
			})
		})

		section(c, "关于", func() {
			settingRow(c, "红果短剧", "版本 0.1.0 · 内容来自红果短剧公开目录", func() {})
		})
	})
}

func (a *app) usageText(n int64) string {
	if a.usage.measuring {
		return "正在计算…"
	}
	return "已占用 " + formatBytes(n)
}

func section(c *ui.Context, title string, rows func()) {
	ui.Column(c.Key("sec-" + title)).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(700).TextColor(colorMuted)
		ui.Column(c).Radius(10).Background(rgba(255, 255, 255, 10)).Border(1, rgba(255, 255, 255, 18)).Children(rows)
	})
}

func settingRow(c *ui.Context, title, detail string, control func()) {
	ui.Row(c.Key("row-"+title)).AlignItems(ui.Center).Gap(20).Padding(14, 18).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(colorFaint)
			}
		})
		control()
	})
}
