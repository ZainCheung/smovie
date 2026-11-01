package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"smovie/internal/hongguo"
)

// watched is a series the user played, and where they were.
type watched struct {
	Series  hongguo.Series `json:"series"`
	Episode int            `json:"episode"` // index of the last episode played
	Total   int            `json:"total"`
	Updated time.Time      `json:"updated"`
}

func historyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "smovie", "history.json")
}

func loadHistory() []watched {
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return nil
	}
	var list []watched
	_ = json.Unmarshal(data, &list)
	return list
}

// saveHistory writes the list on another goroutine; call on the main
// thread with a copy the caller no longer changes.
func saveHistory(list []watched) {
	go func() {
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return
		}
		p := historyPath()
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		tmp := p + ".tmp"
		if os.WriteFile(tmp, data, 0o644) == nil {
			_ = os.Rename(tmp, p)
		}
	}()
}

// recordWatch moves series to the front of the history with its episode.
func recordWatch(list []watched, s hongguo.Series, episode, total int) []watched {
	out := []watched{{Series: s, Episode: episode, Total: total, Updated: time.Now()}}
	for _, w := range list {
		if w.Series.ID != s.ID {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}
