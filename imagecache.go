package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"smovie/internal/hongguo"
)

// imgSize is the size a picture shows at, which chooses the size the CDN
// sends it in.
type imgSize int

const (
	imgSmall imgSize = iota // posters in rows and grids
	imgLarge                // the banner's poster and the blurred backdrops
)

const (
	imgWorkers  = 10
	imgMemMax   = 360       // decoded pictures kept
	imgDiskMax  = 300 << 20 // bytes of pictures kept on disk
	imgFadeTime = 220 * time.Millisecond
)

// imgBackoff is how long a picture that failed waits before trying again.
var imgBackoff = []time.Duration{2 * time.Second, 6 * time.Second, 20 * time.Second, time.Minute, 3 * time.Minute}

type imgState int

const (
	imgDisk    imgState = iota // being looked for on disk
	imgQueued                  // waiting for a download
	imgLoading                 // downloading
	imgReady
	imgFailed
)

type imgEntry struct {
	url, orig string // what is downloaded, and the URL the catalog gave
	state     imgState
	bmp       *ui.Bitmap
	shownAt   time.Time // when a view first had it ready
	released  bool
	failures  int
	retryAt   time.Time
	seenAt    time.Time // when it last showed in the window
	used      uint64    // the cache's tick when last asked for
}

// picture is what a view shows for an image.
type picture struct {
	Img    ui.ImageSource // nil until it loads
	Fade   float32        // its opacity as it fades in
	Failed bool           // it failed, and waits to try again
}

// imageCache loads pictures from the CDN, in the sizes they show at, keeps
// them on disk and the recent ones decoded, and loads first those that
// show in the window. A picture that fails tries again later, by itself.
type imageCache struct {
	dir        string
	invalidate func()

	mu      sync.Mutex
	entries map[string]*imgEntry
	tick    uint64
	wake    chan struct{}
	diskSem chan struct{}
}

func newImageCache(invalidate func()) *imageCache {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	ic := &imageCache{
		dir:        filepath.Join(base, "smovie", "images"),
		invalidate: invalidate,
		entries:    map[string]*imgEntry{},
		wake:       make(chan struct{}, imgWorkers),
		diskSem:    make(chan struct{}, 4),
	}
	_ = os.MkdirAll(ic.dir, 0o755)
	for range imgWorkers {
		go ic.worker()
	}
	go ic.pruneDisk(imgDiskMax)
	return ic
}

var shrinkPattern = regexp.MustCompile(`~tplv-shrink:\d+:\d+\.(?:image|jpe?g|png|webp)$`)

// sizedURL asks the CDN for the picture at the width it shows at on a 2×
// screen, as WebP, smaller than the JPEG it sends otherwise.
func sizedURL(raw string, size imgSize) string {
	w := "360"
	if size == imgLarge {
		w = "720"
	}
	if !shrinkPattern.MatchString(raw) {
		return raw
	}
	return shrinkPattern.ReplaceAllString(raw, "~tplv-shrink:"+w+":0.webp")
}

// Get returns the picture of raw at size, starting to load it the first
// time. visible tells that its element showed in the window, which loads
// it before those that did not. Call it from the view.
func (ic *imageCache) Get(c *ui.Context, raw string, size imgSize, visible bool) picture {
	if raw == "" {
		return picture{Failed: true}
	}
	u := sizedURL(raw, size)
	now := time.Now()
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.tick++
	e := ic.entries[u]
	if e == nil {
		e = &imgEntry{url: u, orig: raw, state: imgDisk}
		ic.entries[u] = e
		go ic.fromDisk(e)
	}
	e.used = ic.tick
	if visible {
		e.seenAt = now
	}
	switch e.state {
	case imgReady:
		if e.shownAt.IsZero() {
			e.shownAt = now
		}
		since := now.Sub(e.shownAt)
		fade := float32(1)
		if since < imgFadeTime {
			fade = float32(since) / float32(imgFadeTime)
			c.After(16 * time.Millisecond)
		} else if !e.released && since > 2*time.Second && e.seenAt.Sub(e.shownAt) > time.Second {
			// Shown by now: the GPU keeps its texture, and the pixels decode
			// again should something need them.
			e.bmp.ReleasePixels()
			e.released = true
		}
		return picture{Img: e.bmp, Fade: max(fade, 0.01)}
	case imgFailed:
		if now.After(e.retryAt) {
			e.state = imgQueued
			ic.signal()
		}
	case imgQueued:
		if visible {
			ic.signal()
		}
	}
	// A large picture shows the small one while it loads.
	if size == imgLarge {
		if s := ic.entries[sizedURL(raw, imgSmall)]; s != nil && s.state == imgReady {
			s.used = ic.tick
			return picture{Img: s.bmp, Fade: 1}
		}
	}
	return picture{Failed: e.state == imgFailed}
}

func (ic *imageCache) signal() {
	select {
	case ic.wake <- struct{}{}:
	default:
	}
}

func (ic *imageCache) path(u string) string {
	sum := sha1.Sum([]byte(u))
	return filepath.Join(ic.dir, hex.EncodeToString(sum[:]))
}

// fromDisk loads e from disk, or queues its download.
func (ic *imageCache) fromDisk(e *imgEntry) {
	ic.diskSem <- struct{}{}
	p := ic.path(e.url)
	data, err := os.ReadFile(p)
	var bmp *ui.Bitmap
	if err == nil {
		bmp, err = ui.DecodeBitmap(data)
	}
	<-ic.diskSem
	if err != nil {
		if len(data) > 0 {
			_ = os.Remove(p) // not a picture
		}
		ic.mu.Lock()
		e.state = imgQueued
		ic.mu.Unlock()
		ic.signal()
		return
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // recently used, for pruning
	ic.ready(e, bmp)
}

func (ic *imageCache) ready(e *imgEntry, bmp *ui.Bitmap) {
	ic.mu.Lock()
	e.bmp, e.state, e.failures = bmp, imgReady, 0
	ic.evictLocked()
	ic.mu.Unlock()
	ic.invalidate()
}

// next takes the queued picture to download first: one that shows in the
// window, then the one asked for last.
func (ic *imageCache) next() *imgEntry {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	recent := time.Now().Add(-time.Second)
	var best *imgEntry
	for _, e := range ic.entries {
		if e.state != imgQueued {
			continue
		}
		if best == nil {
			best = e
			continue
		}
		ev, bv := e.seenAt.After(recent), best.seenAt.After(recent)
		if ev != bv {
			if ev {
				best = e
			}
			continue
		}
		if e.used > best.used {
			best = e
		}
	}
	if best != nil {
		best.state = imgLoading
	}
	return best
}

func (ic *imageCache) worker() {
	for {
		e := ic.next()
		if e == nil {
			<-ic.wake
			continue
		}
		ic.download(e)
	}
}

func (ic *imageCache) download(e *imgEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	data, err := hongguo.FetchImage(ctx, e.url)
	var bmp *ui.Bitmap
	if err == nil {
		bmp, err = ui.DecodeBitmap(data)
	}
	if err != nil && e.url != e.orig {
		// The CDN may not make every size: the picture as the catalog gave it.
		if data, err = hongguo.FetchImage(ctx, e.orig); err == nil {
			bmp, err = ui.DecodeBitmap(data)
		}
	}
	if err != nil {
		ic.mu.Lock()
		wait := imgBackoff[min(e.failures, len(imgBackoff)-1)]
		e.failures++
		e.state, e.retryAt = imgFailed, time.Now().Add(wait)
		ic.mu.Unlock()
		ic.invalidate()
		time.AfterFunc(wait, ic.invalidate) // a view asking again retries
		return
	}
	p := ic.path(e.url)
	if os.WriteFile(p+".tmp", data, 0o644) == nil {
		_ = os.Rename(p+".tmp", p)
	}
	ic.ready(e, bmp)
}

// evictLocked forgets the pictures asked for longest ago once more than
// imgMemMax are decoded; those still showing stay.
func (ic *imageCache) evictLocked() {
	var ready []*imgEntry
	for _, e := range ic.entries {
		if e.state == imgReady {
			ready = append(ready, e)
		}
	}
	if len(ready) <= imgMemMax {
		return
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].used < ready[j].used })
	recent := time.Now().Add(-2 * time.Second)
	for _, e := range ready[:len(ready)-imgMemMax*9/10] {
		if e.seenAt.Before(recent) {
			delete(ic.entries, e.url)
		}
	}
}

// pruneDisk removes the pictures used longest ago once they take more
// than limit bytes.
func (ic *imageCache) pruneDisk(limit int64) {
	entries, err := os.ReadDir(ic.dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, d := range entries {
		info, err := d.Info()
		if err != nil || d.IsDir() {
			continue
		}
		files = append(files, file{filepath.Join(ic.dir, d.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= limit*8/10 {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// diskUsage is the bytes of pictures on disk.
func (ic *imageCache) diskUsage() int64 { return dirSize(ic.dir) }

// clearDisk removes the pictures on disk; those decoded stay.
func (ic *imageCache) clearDisk() { ic.pruneDisk(0) }

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
