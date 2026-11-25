package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// settingsPage holds playback choices and what the caches take on disk.
func (a *app) settingsPage(c *ui.Context) {
	ui.Column(c).Gap(26).Padding(navHeight+28, pagePad, 56, pagePad).MaxWidth(860).Children(func() {
		ui.Text(c, "设置").FontSize(28).FontWeight(800)

		section(c, "播放", func() {
			settingRow(c, "自动连播", "一集播完后自动播放下一集", func() {
				ui.Switch(c.Key("auto"), &a.autoNext).Label("自动连播")
			})
			settingRow(c, "长按锁定倍速", "按住 → 或空格 3 秒后保持 2x 倍速，按任意键退出", func() {
				ui.Switch(c.Key("hold-lock"), &a.holdLock).Label("长按锁定倍速")
			})
		})

		if runtime.GOOS == "darwin" {
			section(c, "实验性功能", func() {
				settingRow(c, "全屏剧集面板", panelFullDetail, func() {
					ui.Switch(c.Key("panel-full"), &a.panelFull).Label("全屏剧集面板")
				})
			})
		}

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

		section(c, "取源与协议", func() {
			a.protocolRows(c)
		})

		section(c, "观看记录", func() {
			settingRow(c, "观看记录", fmt.Sprintf("共 %d 部", len(a.history)), func() {
				if ui.Button(c.Key("manage-history"), "管理").Clicked() {
					a.go_("history")
					a.scroll("history").Y = 0
				}
			})
		})

		section(c, "关于", func() { a.aboutRows(c) })
	})
}

const panelFullDetail = "全屏时左上角显示剧集标题，点击后在右侧打开选集与系列剧面板。开启后由应用窗口进入全屏，而非网页播放器自身的全屏"

func (a *app) usageText(n int64) string {
	if a.usage.measuring {
		return "正在计算…"
	}
	return "已占用 " + formatBytes(n)
}

func (a *app) protocolRows(c *ui.Context) {
	st := a.protocolStatus
	if st.Version == "" {
		st = hongguo.CurrentProtocolStatus()
		a.protocolStatus = st
	}
	sourceLabel := map[string]string{
		"embedded": "内置基线",
		"cached":   "本地热更缓存",
		"remote":   "远程已验签热更",
	}[st.Source]
	if sourceLabel == "" {
		sourceLabel = "内置基线"
	}
	detail := fmt.Sprintf("策略版本 %s · %s", st.Version, sourceLabel)
	if a.protocolChecking {
		detail = "正在检查并校验协议策略…"
	} else if st.LastError != "" {
		detail += " · 上次检查失败：" + st.LastError
	}
	settingRow(c, "取源协议策略", detail, func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			checkBtn := ui.Button(c.Key("protocol-refresh"), "检查策略更新").Disabled(a.protocolChecking)
			if checkBtn.Clicked() {
				a.refreshProtocolAsync(c)
			}
			if st.Source != "embedded" {
				if ui.Button(c.Key("protocol-reset"), "恢复默认").Clicked() {
					a.protocolStatus = hongguo.ResetProtocol()
					c.Toast("已恢复内置协议基线")
				}
			}
		})
	})

	signerDetail := "留空使用内置纯 Go 签名；内置签名失效时可填入自建 openhongguo-signer 地址应急"
	if a.saved.SignerURL != "" {
		signerDetail = "当前已启用远程签名：" + a.saved.SignerURL
	}
	ui.Column(c.Key("row-remote-signer")).Gap(10).Padding(14, 18).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "远程签名服务（可选）").FontSize(15).FontWeight(600)
			ui.Text(c, signerDetail).FontSize(12).TextColor(colorFaint)
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("signer-url"), &a.signerURL).Placeholder("https://signer.example.com 或 http://127.0.0.1:19830").
				Label("签名服务地址").Grow(1).OnSubmit(func() { a.applySignerSettings(c) })
			ui.TextInput(c.Key("signer-token"), &a.signerToken).Placeholder("Token（可选）").
				Label("签名服务令牌").Width(180).OnSubmit(func() { a.applySignerSettings(c) })
			if ui.Button(c.Key("signer-apply"), "应用").Clicked() {
				a.applySignerSettings(c)
			}
		})
	})
}

func (a *app) refreshProtocolAsync(c *ui.Context) {
	if a.protocolChecking {
		return
	}
	a.protocolChecking = true
	win := a.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, err := hongguo.RefreshProtocol(ctx)
		if win != nil {
			win.Update(func() {
				a.protocolChecking = false
				a.protocolStatus = st
			})
		}
		_ = err
	}()
	c.Toast("正在检查最新协议策略…")
}

func (a *app) applySignerSettings(c *ui.Context) {
	u := strings.TrimSpace(a.signerURL)
	tok := strings.TrimSpace(a.signerToken)
	if err := hongguo.SetSignerConfig(u, tok); err != nil {
		c.Toast(err.Error())
		return
	}
	a.signerURL, a.signerToken = u, tok
	a.saved.SignerURL, a.saved.SignerToken = u, tok
	saveSettings(a.saved)
	if u == "" {
		c.Toast("已切换回内置签名")
	} else {
		c.Toast("已启用远程签名服务")
	}
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
