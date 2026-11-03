package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	keep  atomic.Value // string: the key of the episode playing, which stays
	// quals are the resolutions each episode has, by vid, once resolved.
	quals map[string][]int
}

// Episodes are kept by key: the vid for the highest resolution, or
// vid_px for the highest up to px, the user's choice of quality.
func mediaKey(vid string, px int) string {
	if px <= 0 {
		return vid
	}
	return vid + "_" + strconv.Itoa(px)
}

func splitKey(key string) (vid string, px int) {
	vid, p, _ := strings.Cut(key, "_")
	px, _ = strconv.Atoi(p)
	return vid, px
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
	// stream tells that the episode's file grows in place and plays while
	// it does, ready the bytes at its start that are final.
	stream atomic.Bool
	ready  atomic.Int64

	sigMu sync.Mutex
	sig   chan struct{}
}

// changed returns a channel that closes when the job next moves on.
func (j *job) changed() <-chan struct{} {
	j.sigMu.Lock()
	defer j.sigMu.Unlock()
	if j.sig == nil {
		j.sig = make(chan struct{})
	}
	return j.sig
}

func (j *job) notify() {
	j.sigMu.Lock()
	defer j.sigMu.Unlock()
	if j.sig != nil {
		close(j.sig)
		j.sig = nil
	}
}

// Playable reports whether the episode plays: it is ready, or it streams.
func (j *job) Playable() bool {
	if j.Finished() {
		return j.err == nil
	}
	return j.stream.Load()
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

// resolvePlay finds where an episode's media is; tests stand in for it.
var resolvePlay = hongguo.ResolvePlay

func newLibrary() *library {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	l := &library{dir: filepath.Join(base, "smovie", "media"), jobs: map[string]*job{}, quals: map[string][]int{}}
	l.limit.Store(videoCacheSizes[defaultVideoCache])
	_ = os.MkdirAll(l.dir, 0o755)
	// Episodes a quit cut off half way.
	parts, _ := filepath.Glob(filepath.Join(l.dir, "*.part"))
	for _, p := range parts {
		_ = os.Remove(strings.TrimSuffix(p, ".part") + ".mp4")
		_ = os.Remove(p)
	}
	return l
}

func (l *library) path(key string) string { return filepath.Join(l.dir, key+".mp4") }

// partPath marks an episode whose file is still being written: it streams
// in place, as renaming a file a player has open fails on Windows.
func (l *library) partPath(key string) string { return filepath.Join(l.dir, key+".part") }

func (l *library) cached(key string) bool {
	st, err := os.Stat(l.path(key))
	if err != nil || st.Size() == 0 {
		return false
	}
	_, err = os.Stat(l.partPath(key))
	return os.IsNotExist(err)
}

// streaming returns the job of key while its file grows in place.
func (l *library) streaming(key string) *job {
	l.mu.Lock()
	j := l.jobs[key]
	l.mu.Unlock()
	if j == nil || !j.stream.Load() {
		return nil
	}
	return j
}

// ensure starts preparing key unless it is on disk or on its way.
func (l *library) ensure(key string) *job {
	l.mu.Lock()
	defer l.mu.Unlock()
	if j, ok := l.jobs[key]; ok && (!j.Finished() || j.err == nil) {
		return j
	}
	j := &job{done: make(chan struct{})}
	l.jobs[key] = j
	if l.cached(key) {
		now := time.Now()
		_ = os.Chtimes(l.path(key), now, now) // used again: pruned last
		j.phase.Store("ready")
		close(j.done)
		return j
	}
	j.phase.Store("resolving")
	go func() {
		defer j.notify()
		defer close(j.done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		vid, px := splitKey(key)
		info, err := resolvePlay(ctx, vid, px)
		if err != nil {
			j.err = err
			j.phase.Store("error")
			return
		}
		l.setQualities(vid, info.Qualities)
		j.quality.Store(info.Quality)
		j.width.Store(int64(info.Width))
		j.height.Store(int64(info.Height))
		j.phase.Store("downloading")
		part := l.partPath(key)
		if err := os.WriteFile(part, nil, 0o644); err != nil {
			j.err = err
			j.phase.Store("error")
			return
		}
		err = hongguo.Download(ctx, info, l.path(key), func(p hongguo.Progress) {
			j.received.Store(p.Received)
			j.total.Store(p.Total)
			j.ready.Store(p.Ready)
			if p.Stream {
				j.stream.Store(true)
			}
			if p.Decrypting {
				j.phase.Store("decrypting")
			}
			j.notify()
		})
		if err != nil {
			j.err = err
			j.phase.Store("error")
			_ = os.Remove(l.path(key)) // fails while a player has it open on Windows: the .part stays and marks it
			if _, serr := os.Stat(l.path(key)); os.IsNotExist(serr) {
				_ = os.Remove(part)
			}
			return
		}
		_ = os.Remove(part)
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
func (l *library) setPlaying(key string) { l.keep.Store(key) }

// busy reports whether key plays or is being prepared.
func (l *library) busy(key string) bool {
	if k, _ := l.keep.Load().(string); k == key {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	j := l.jobs[key]
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
		path, key string
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
		if l.busy(f.key) {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			l.mu.Lock()
			if j := l.jobs[f.key]; j != nil && j.Finished() {
				delete(l.jobs, f.key) // prepares it again when asked
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
	// Qualities are the resolutions the episode has, highest first, once
	// known.
	Qualities []int `json:"qualities,omitempty"`
	// Stream tells that the episode plays while it downloads.
	Stream bool   `json:"stream,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (l *library) status(key string) episodeStatus {
	vid, _ := splitKey(key)
	l.mu.Lock()
	j := l.jobs[key]
	quals := l.quals[vid]
	l.mu.Unlock()
	if j == nil {
		if l.cached(key) {
			return episodeStatus{Phase: "ready", Qualities: quals}
		}
		return episodeStatus{Phase: "resolving", Qualities: quals}
	}
	st := episodeStatus{Phase: j.Phase(), Received: j.received.Load(), Total: j.total.Load(), Quality: j.Quality(), Stream: j.stream.Load(), Qualities: quals}
	if j.Finished() && j.err != nil {
		st.Phase, st.Error = "error", j.err.Error()
	}
	return st
}

// size returns the picture's size, when known before it plays.
func (l *library) size(key string) (int, int) {
	l.mu.Lock()
	j := l.jobs[key]
	l.mu.Unlock()
	if j == nil {
		return 0, 0
	}
	return int(j.width.Load()), int(j.height.Load())
}

// knownQualities returns the resolutions vid has, when known.
func (l *library) knownQualities(vid string) []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quals[vid]
}

func (l *library) setQualities(vid string, q []int) {
	if len(q) == 0 {
		return
	}
	l.mu.Lock()
	l.quals[vid] = q
	l.mu.Unlock()
}

// qualities returns the resolutions vid has, asking the API when no job
// told them, as for an episode played from the disk.
func (l *library) qualities(ctx context.Context, vid string) []int {
	l.mu.Lock()
	q, ok := l.quals[vid]
	l.mu.Unlock()
	if ok {
		return q
	}
	info, err := resolvePlay(ctx, vid, 0)
	if err != nil {
		return nil
	}
	l.setQualities(vid, info.Qualities)
	return info.Qualities
}
