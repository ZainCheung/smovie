package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smovie/internal/hongguo"
)

// library keeps decrypted episodes on disk and the jobs preparing them, so
// the episode playing and the one prefetched after it share one download.
type library struct {
	dir   string
	mu    sync.Mutex
	jobs  map[string]*job
	limit atomic.Int64 // bytes of episodes kept on disk
	keep  atomic.Value // string: the episode playing, which stays
}

// job prepares one episode. Its fields other than done are safe to read
// from any goroutine; err is set before done closes.
type job struct {
	done     chan struct{}
	err      error
	phase    atomic.Value // string
	received atomic.Int64
	total    atomic.Int64
	quality  atomic.Value // string
	width    atomic.Int64
	height   atomic.Int64
}

func (j *job) Phase() string {
	s, _ := j.phase.Load().(string)
	return s
}

func (j *job) Quality() string {
	s, _ := j.quality.Load().(string)
	return s
}

func (j *job) Finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

func newLibrary() *library {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	l := &library{dir: filepath.Join(base, "smovie", "media"), jobs: map[string]*job{}}
	l.limit.Store(videoCacheSizes[defaultVideoCache])
	_ = os.MkdirAll(l.dir, 0o755)
	return l
}

func (l *library) path(vid string) string { return filepath.Join(l.dir, vid+".mp4") }

func (l *library) cached(vid string) bool {
	st, err := os.Stat(l.path(vid))
	return err == nil && st.Size() > 0
}

// ensure starts preparing vid unless it is on disk or on its way.
func (l *library) ensure(vid string) *job {
	l.mu.Lock()
	defer l.mu.Unlock()
	if j, ok := l.jobs[vid]; ok && (!j.Finished() || j.err == nil) {
		return j
	}
	j := &job{done: make(chan struct{})}
	l.jobs[vid] = j
	if l.cached(vid) {
		now := time.Now()
		_ = os.Chtimes(l.path(vid), now, now) // used again: pruned last
		j.phase.Store("ready")
		close(j.done)
		return j
	}
	j.phase.Store("resolving")
	go func() {
		defer close(j.done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		info, err := hongguo.ResolvePlay(ctx, vid)
		if err != nil {
			j.err = err
			j.phase.Store("error")
			return
		}
		j.quality.Store(info.Quality)
		j.width.Store(int64(info.Width))
		j.height.Store(int64(info.Height))
		j.phase.Store("downloading")
		err = hongguo.Download(ctx, info, l.path(vid), func(received, total int64) {
			j.received.Store(received)
			j.total.Store(total)
			if total > 0 && received >= total && info.ContentKey != "" {
				j.phase.Store("decrypting")
			}
		})
		if err != nil {
			j.err = err
			j.phase.Store("error")
			return
		}
		j.phase.Store("ready")
		go l.prune()
	}()
	return j
}

// setLimit keeps at most limit bytes of episodes on disk.
func (l *library) setLimit(limit int64) {
	l.limit.Store(limit)
	go l.prune()
}

// setPlaying tells the episode playing, which pruning leaves.
func (l *library) setPlaying(vid string) { l.keep.Store(vid) }

// busy reports whether vid plays or is being prepared.
func (l *library) busy(vid string) bool {
	if k, _ := l.keep.Load().(string); k == vid {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	j := l.jobs[vid]
	return j != nil && !j.Finished()
}

// prune removes the episodes used longest ago once they take more than
// the limit, leaving those that play or are being prepared.
func (l *library) prune() { l.pruneTo(l.limit.Load()) }

func (l *library) pruneTo(limit int64) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	type file struct {
		path, vid string
		size      int64
		mod       time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".mp4" {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{filepath.Join(l.dir, e.Name()), strings.TrimSuffix(e.Name(), ".mp4"), info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= limit {
			break
		}
		if l.busy(f.vid) {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			l.mu.Lock()
			if j := l.jobs[f.vid]; j != nil && j.Finished() {
				delete(l.jobs, f.vid) // prepares it again when asked
			}
			l.mu.Unlock()
		}
	}
}

// clear removes the episodes on disk, but for those that play or are
// being prepared.
func (l *library) clear() { l.pruneTo(0) }

// usage is the bytes of episodes on disk.
func (l *library) usage() int64 { return dirSize(l.dir) }

// episodeStatus is what the player page shows while an episode is prepared.
type episodeStatus struct {
	Phase    string `json:"phase"` // resolving, downloading, decrypting, ready, error
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Quality  string `json:"quality"`
	Error    string `json:"error,omitempty"`
}

func (l *library) status(vid string) episodeStatus {
	l.mu.Lock()
	j := l.jobs[vid]
	l.mu.Unlock()
	if j == nil {
		if l.cached(vid) {
			return episodeStatus{Phase: "ready"}
		}
		return episodeStatus{Phase: "resolving"}
	}
	st := episodeStatus{Phase: j.Phase(), Received: j.received.Load(), Total: j.total.Load(), Quality: j.Quality()}
	if j.Finished() && j.err != nil {
		st.Phase, st.Error = "error", j.err.Error()
	}
	return st
}

// size returns the picture's size, when known before it plays.
func (l *library) size(vid string) (int, int) {
	l.mu.Lock()
	j := l.jobs[vid]
	l.mu.Unlock()
	if j == nil {
		return 0, 0
	}
	return int(j.width.Load()), int(j.height.Load())
}
