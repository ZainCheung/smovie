package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"smovie/internal/hongguo"
)

const historyMax = 200

// watched is a series the user played, and where they were.
type watched struct {
	Series   hongguo.Series `json:"series"`
	Episode  int            `json:"episode"` // index of the last episode played
	Total    int            `json:"total"`
	Position float64        `json:"position,omitempty"` // seconds into the episode
	Duration float64        `json:"duration,omitempty"` // of the episode, once known
	Finished bool           `json:"finished,omitempty"` // the last episode played to its end
	Updated  time.Time      `json:"updated"`
}

// progress is how much of the series was watched, from 0 to 1.
func (w watched) progress() float32 {
	if w.Finished {
		return 1
	}
	if w.Total <= 0 {
		return 0
	}
	part := float64(0)
	if w.Duration > 0 {
		part = min(w.Position/w.Duration, 1)
	}
	return float32((float64(w.Episode) + part) / float64(w.Total))
}

// resumeAt is where the episode index ep picks up: where it was left,
// unless that was at its very start or end.
func (w watched) resumeAt(ep int) float64 {
	if w.Episode != ep || w.Position < 5 || (w.Duration > 0 && w.Position > w.Duration-8) {
		return 0
	}
	return w.Position
}

// historyFile, when set, is where the history is kept instead: for tests.
var historyFile string

func historyPath() string {
	if historyFile != "" {
		return historyFile
	}
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

var (
	historyMu  sync.Mutex
	historyGen int // the last list given to saveHistory
)

// saveHistory writes the list on another goroutine; call on the main
// thread with a copy the caller no longer changes. A list given after it
// wins over it, whichever goroutine runs first.
func saveHistory(list []watched) {
	historyMu.Lock()
	historyGen++
	gen := historyGen
	historyMu.Unlock()
	go func() {
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return
		}
		historyMu.Lock()
		defer historyMu.Unlock()
		if gen != historyGen {
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

// recordWatch moves series to the front of the history with its episode,
// keeping where the episode was left when it is the one it was.
func recordWatch(list []watched, s hongguo.Series, episode, total int) []watched {
	w := watched{Series: s, Episode: episode, Total: total, Updated: time.Now()}
	out := []watched{w}
	for _, old := range list {
		if old.Series.ID != s.ID {
			out = append(out, old)
		} else if old.Episode == episode {
			out[0].Position, out[0].Duration = old.Position, old.Duration
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > historyMax {
		out = out[:historyMax]
	}
	return out
}

// recordProgress notes where episode of the series is, and whether the
// series ended; it reports whether the list changed.
func recordProgress(list []watched, id string, episode int, pos, dur float64, ended bool) bool {
	for i := range list {
		w := &list[i]
		if w.Series.ID != id || w.Episode != episode {
			continue
		}
		w.Position, w.Duration, w.Updated = pos, dur, time.Now()
		if ended && w.Total > 0 && episode >= w.Total-1 {
			w.Finished, w.Position = true, 0
		}
		return true
	}
	return false
}

// removeWatch takes the series out of the list, returning the new list,
// and the entry and where it was, to put it back.
func removeWatch(list []watched, id string) ([]watched, watched, int) {
	for i, w := range list {
		if w.Series.ID == id {
			out := append(append([]watched(nil), list[:i]...), list[i+1:]...)
			return out, w, i
		}
	}
	return list, watched{}, -1
}

// restoreWatch puts back an entry removeWatch took out, unless the series
// was played again since.
func restoreWatch(list []watched, w watched, at int) []watched {
	for _, x := range list {
		if x.Series.ID == w.Series.ID {
			return list
		}
	}
	at = max(0, min(at, len(list)))
	out := append(append(append([]watched(nil), list[:at]...), w), list[at:]...)
	return out
}
