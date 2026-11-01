package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

var (
	colorBG     = rgba(20, 20, 20, 255)
	colorRed    = rgba(229, 9, 20, 255)
	colorWhite  = rgba(255, 255, 255, 255)
	colorMuted  = rgba(178, 178, 178, 255)
	colorFaint  = rgba(128, 128, 128, 255)
	colorCard   = rgba(38, 38, 38, 255)
	colorGlass  = rgba(109, 109, 110, 170)
	colorShadow = rgba(0, 0, 0, 140)
)

// netflixTheme is a dark theme in the manner of streaming services.
func netflixTheme() *ui.Theme {
	t := *ui.DarkTheme()
	t.Background = colorBG
	t.Surface = colorCard
	t.SurfaceHover = rgba(56, 56, 56, 255)
	t.SurfacePressed = rgba(70, 70, 70, 255)
	t.Border = rgba(64, 64, 64, 255)
	t.Text = colorWhite
	t.TextMuted = colorMuted
	t.Accent = colorRed
	t.AccentHover = rgba(244, 6, 18, 255)
	t.AccentPressed = rgba(193, 17, 25, 255)
	t.AccentText = colorWhite
	t.Focus = rgba(255, 255, 255, 200)
	t.Radius = 6
	return &t
}

// rgba is a color with alpha from 0 to 255.
func rgba(r, g, b uint8, a float32) ui.Color { return ui.RGBA(r, g, b, a/255) }

func withAlpha(c ui.Color, a uint8) ui.Color {
	c.A = a
	return c
}

func (a *app) view(c *ui.Context) {
	if a.player == nil {
		return // the window can draw before its web view is made
	}
	c.SetTheme(a.theme)
	c.Root().Background(colorBG)
	if a.theater && a.page == "watch" {
		c.Root().Background(rgba(0, 0, 0, 255))
		ui.WebView(c, a.player).Fill()
		c.OnShortcut(0, ui.KeyEscape, func() { a.setTheater(false) })
		return
	}
	st := a.scroll(a.page)
	ui.Box(c).Fill().Children(func() {
		ui.Scroll(c.Key(a.page)).Fill().TrackScroll(st).Children(func() {
			switch a.page {
			case "home":
				a.homePage(c)
			case "watch":
				a.watchPage(c)
			case "search":
				a.searchPage(c)
			default:
				a.categoryPage(c, a.page)
			}
		})
		a.navBar(c, st.Y)
	})
}

// navBar floats over the page: clear over the banner, solid once the page
// scrolls, as streaming services have it.
func (a *app) navBar(c *ui.Context, scrolled float32) {
	bar := c.TitleBar()
	nav := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(navHeight).AlignItems(ui.Center).Gap(22).
		Padding(0, 28, 0, bar.Left+20).DragWindow()
	solid := nav.Animate("solid", b2f(scrolled > 24), 220*time.Millisecond)
	// A shade fading down over the banner, turning into a solid bar.
	nav.LinearGradient(ui.LinearGradient{Angle: 180,
		From: rgba(10, 10, 10, 170+80*solid), To: rgba(20, 20, 20, 250*solid)})
	nav.Children(func() {
		if a.page == "watch" {
			back := ui.ButtonBase(c.Key("back")).Size(32, 32).Radius(16).Center().Label("返回").Tooltip("返回")
			back.Background(withAlpha(colorWhite, uint8(map[bool]int{true: 46, false: 22}[back.Hovered()])))
			back.Children(func() { ui.Icon(c, iconBack).Size(18, 18).TextColor(colorWhite) })
			if back.Clicked() {
				a.go_(a.backTo)
			}
		}
		logo := ui.Row(c.Key("logo")).AlignItems(ui.Center).Gap(2).Cursor(ui.CursorPointer)
		if logo.Clicked() {
			a.go_("home")
		}
		logo.Children(func() {
			ui.Text(c, "红果").FontSize(24).FontWeight(900).TextColor(colorRed).LetterSpacing(1)
			ui.Text(c, "短剧").FontSize(24).FontWeight(300).TextColor(colorWhite)
		})
		ui.Row(c).Gap(18).Margin(0, 0, 0, 14).Children(func() {
			a.navTab(c, "home", "首页")
			for _, cat := range hongguo.Categories {
				a.navTab(c, cat.Slug, cat.Label)
			}
		})
		ui.Spacer(c)
		ui.SearchField(c.Key("search"), &a.query).Placeholder("片名、题材、演员").Label("搜索").Width(240).
			OnSubmit(func() { a.search(a.query) })
	})
}

func (a *app) navTab(c *ui.Context, page, label string) {
	active := a.page == page || (a.page == "watch" && a.backTo == page)
	tab := ui.ButtonBase(c.Key("tab-"+page)).Padding(6, 2).Label(label)
	tab.Children(func() {
		t := ui.Text(c, label).FontSize(14).SingleLine()
		switch {
		case active:
			t.FontWeight(700).TextColor(colorWhite)
		case tab.Hovered():
			t.TextColor(rgba(220, 220, 220, 255))
		default:
			t.TextColor(colorMuted)
		}
	})
	if tab.Clicked() {
		a.go_(page)
		a.scroll(page).Y = 0
	}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
