package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	videoCacheSizes  = []int64{1 << 30, 2 << 30, 5 << 30, 10 << 30}
	videoCacheLabels = []string{"1 GB", "2 GB", "5 GB", "10 GB"}
)

const defaultVideoCache = 1

// settings are the user's choices, kept across launches.
type settings struct {
	AutoNext   bool `json:"autoNext"`
	VideoCache int  `json:"videoCache"` // index of videoCacheSizes
}

func defaultSettings() settings { return settings{AutoNext: true, VideoCache: defaultVideoCache} }

// configOverride, when set, is where settings and history are kept
// instead: for tests.
var configOverride string

func configDir() string {
	if configOverride != "" {
		return configOverride
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "smovie")
}

func loadSettings() settings {
	s := defaultSettings()
	if data, err := os.ReadFile(filepath.Join(configDir(), "settings.json")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.VideoCache < 0 || s.VideoCache >= len(videoCacheSizes) {
		s.VideoCache = defaultVideoCache
	}
	return s
}

var settingsMu sync.Mutex

func saveSettings(s settings) {
	go func() {
		settingsMu.Lock()
		defer settingsMu.Unlock()
		data, _ := json.MarshalIndent(s, "", "  ")
		p := filepath.Join(configDir(), "settings.json")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if os.WriteFile(p+".tmp", data, 0o644) == nil {
			_ = os.Rename(p+".tmp", p)
		}
	}()
}

// syncSettings saves the settings once they changed; the view calls it,
// as switches change them in place.
func (a *app) syncSettings() {
	cur := settings{AutoNext: a.autoNext, VideoCache: a.videoCache}
	if cur == a.saved {
		return
	}
	if cur.VideoCache != a.saved.VideoCache {
		a.lib.setLimit(videoCacheSizes[cur.VideoCache])
	}
	a.saved = cur
	saveSettings(cur)
}

// usage is what the caches take on disk, measured off the main thread.
type usage struct {
	images, videos int64
	measuring      bool
}

func (a *app) measureUsage() {
	if a.usage.measuring {
		return
	}
	a.usage.measuring = true
	go func() {
		img, vid := a.images.diskUsage(), a.lib.usage()
		a.win.Update(func() { a.usage = usage{images: img, videos: vid} })
	}()
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
