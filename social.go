package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"smovie/internal/hongguo"
)

// loaded is a value the watch page loads from the site, once, or again
// after it failed and is asked again.
type loaded[T any] struct {
	value   *T
	err     error
	loading bool
}

// seriesInfo returns what the site tells of a series beyond its episodes,
// starting to load it.
func (a *app) seriesInfo(id string) *loaded[hongguo.SeriesInfo] {
	l, ok := a.infos[id]
	if !ok {
		l = &loaded[hongguo.SeriesInfo]{}
		a.infos[id] = l
		a.loadInfo(id, l)
	}
	return l
}

func (a *app) loadInfo(id string, l *loaded[hongguo.SeriesInfo]) {
	l.loading, l.err = true, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		v, err := hongguo.Info(ctx, id)
		a.win.Update(func() { l.value, l.err, l.loading = v, err, false })
	}()
}

// episodeInfo returns an episode's likes and comments, starting to load
// them.
func (a *app) episodeInfo(seriesID, vid string) *loaded[hongguo.EpisodeInfo] {
	l, ok := a.epInfos[vid]
	if !ok {
		l = &loaded[hongguo.EpisodeInfo]{}
		a.epInfos[vid] = l
		a.loadEpisodeInfo(seriesID, vid, l)
	}
	return l
}

func (a *app) loadEpisodeInfo(seriesID, vid string, l *loaded[hongguo.EpisodeInfo]) {
	l.loading, l.err = true, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		v, err := hongguo.Episode(ctx, seriesID, vid)
		a.win.Update(func() { l.value, l.err, l.loading = v, err, false })
	}()
}

func isLocked(err error) bool { return errors.Is(err, hongguo.ErrLocked) }

// count is a number as Chinese apps show it: 7,083, 1.5万, 3.2亿.
func count(n int) string {
	switch {
	case n >= 1e8:
		return trimZero(float64(n)/1e8) + "亿"
	case n >= 1e4:
		return trimZero(float64(n)/1e4) + "万"
	case n >= 1000:
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return strconv.Itoa(n)
}

func trimZero(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}

// ago is how long ago t was, as "3 天前".
func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d 个月前", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%d 年前", int(now.Sub(t).Hours()/24/365))
}
