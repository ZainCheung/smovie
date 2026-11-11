package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"smovie/internal/hongguo"
)

func TestParseRange(t *testing.T) {
	for _, c := range []struct {
		h          string
		start, end int64
		ok         bool
	}{
		{"", 0, 100, true},
		{"bytes=0-1", 0, 2, true},
		{"bytes=10-", 10, 100, true},
		{"bytes=90-200", 90, 100, true},
		{"bytes=-10", 90, 100, true},
		{"bytes=100-", 0, 0, false},
		{"bytes=5-2", 0, 0, false},
		{"bytes=0-1,4-5", 0, 0, false},
		{"items=0-1", 0, 0, false},
	} {
		start, end, ok := parseRange(c.h, 100)
		if ok != c.ok || ok && (start != c.start || end != c.end) {
			t.Errorf("%q: got %d-%d %v, want %d-%d %v", c.h, start, end, ok, c.start, c.end, c.ok)
		}
	}
}

// growing is a job writing a file in place, a piece at a time.
func growing(t *testing.T, data []byte) (*job, string, func(n int)) {
	path := filepath.Join(t.TempDir(), "1.mp4")
	if err := os.WriteFile(path, make([]byte, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	j := &job{done: make(chan struct{})}
	j.total.Store(int64(len(data)))
	j.stream.Store(true)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return j, path, func(n int) {
		at := j.ready.Load()
		if _, err := f.WriteAt(data[at:at+int64(n)], at); err != nil {
			t.Error(err)
		}
		j.ready.Add(int64(n))
		j.notify()
	}
}

func TestServeGrowing(t *testing.T) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	j, path, write := growing(t, data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveGrowing(w, r, j, path)
	}))
	defer srv.Close()

	get := func(rng string) (*http.Response, []byte, error) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		return res, b, err
	}

	write(1000)
	// What is there comes at once.
	res, b, err := get("bytes=0-99")
	if err != nil || res.StatusCode != http.StatusPartialContent || !bytes.Equal(b, data[:100]) {
		t.Fatalf("head: %v %v %d bytes", err, res.Status, len(b))
	}
	if got := res.Header.Get("Content-Range"); got != "bytes 0-99/1048576" {
		t.Fatalf("Content-Range %q", got)
	}
	// The rest comes as it is written.
	go func() {
		for j.ready.Load() < int64(len(data)) {
			time.Sleep(time.Millisecond)
			write(min(64<<10, len(data)-int(j.ready.Load())))
		}
		close(j.done)
	}()
	res, b, err = get("")
	if err != nil || res.StatusCode != http.StatusOK || !bytes.Equal(b, data) {
		t.Fatalf("all: %v %d bytes", err, len(b))
	}
	if _, b, _ = get("bytes=-5"); !bytes.Equal(b, data[len(data)-5:]) {
		t.Fatalf("tail: %v", b)
	}
}

func TestServeGrowingFails(t *testing.T) {
	data := make([]byte, 4096)
	j, path, write := growing(t, data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveGrowing(w, r, j, path)
	}))
	defer srv.Close()
	write(100)
	go func() {
		time.Sleep(20 * time.Millisecond)
		j.err = errors.New("gone")
		close(j.done)
		j.notify()
	}()
	res, err := http.Get(srv.URL)
	if err != nil {
		return // cut off before its headers went out
	}
	defer res.Body.Close()
	if b, err := io.ReadAll(res.Body); err == nil {
		t.Fatalf("read %d bytes of a failed download without an error", len(b))
	}
}

func TestMediaKey(t *testing.T) {
	for _, c := range []struct {
		vid string
		px  int
		key string
	}{{"123", 0, "123"}, {"123", 720, "123_720"}} {
		key := mediaKey(c.vid, c.px)
		if key != c.key || !keyPattern.MatchString(key) {
			t.Errorf("mediaKey(%s, %d) = %s", c.vid, c.px, key)
		}
		if vid, px := splitKey(key); vid != c.vid || px != c.px {
			t.Errorf("splitKey(%s) = %s, %d", key, vid, px)
		}
	}
}

func TestDownloadUsesBackupWhenPrimaryIsForbidden(t *testing.T) {
	data := []byte("clear-media-fixture")
	var primary, backup atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "fixture-player" {
			t.Error("rendition user agent was lost")
		}
		if r.URL.Path == "/primary" {
			primary.Add(1)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		backup.Add(1)
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.mp4")
	info := &hongguo.PlayInfo{URL: srv.URL + "/primary", BackupURLs: []string{srv.URL + "/primary", srv.URL + "/backup"}, UserAgent: "fixture-player"}
	if err := hongguo.Download(context.Background(), info, dst, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) || primary.Load() != 1 || backup.Load() != 1 {
		t.Fatalf("data=%q err=%v primary=%d backup=%d", got, err, primary.Load(), backup.Load())
	}
}

func TestDownloadRejectedAddressesAreBounded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	info := &hongguo.PlayInfo{URL: srv.URL + "/0", BackupURLs: []string{srv.URL + "/0", srv.URL + "/1", srv.URL + "/2", srv.URL + "/3"}}
	dst := filepath.Join(t.TempDir(), "out.mp4")
	err := hongguo.Download(context.Background(), info, dst, func(hongguo.Progress) {
		t.Error("rejected response published download progress")
	})
	if !errors.Is(err, hongguo.ErrMediaUnavailable) || calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("rejection left an output file: %v", err)
	}
	calls.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hongguo.Download(ctx, info, dst, nil); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("canceled download: err=%v calls=%d", err, calls.Load())
	}
}

// A CDN rejection used to end a job immediately. Recovery must refresh the
// exact episode/quality once, but never loop or restart an accepted body.
func TestLibraryRefreshesRejectedMediaOnce(t *testing.T) {
	for _, tc := range []struct {
		name         string
		firstStatus  int
		freshStatus  int
		shortBody    bool
		resolveError bool
		wantResolves int
		wantRequests int
		wantOK       bool
	}{
		{name: "403 then fresh address", firstStatus: 403, freshStatus: 200, wantResolves: 2, wantRequests: 2, wantOK: true},
		{name: "all addresses remain forbidden", firstStatus: 403, freshStatus: 403, wantResolves: 2, wantRequests: 2},
		{name: "gone address", firstStatus: 410, freshStatus: 200, wantResolves: 2, wantRequests: 2, wantOK: true},
		{name: "unauthorized address", firstStatus: 401, freshStatus: 200, wantResolves: 2, wantRequests: 2, wantOK: true},
		{name: "missing address", firstStatus: 404, freshStatus: 200, wantResolves: 2, wantRequests: 2, wantOK: true},
		{name: "server error is not an address refresh", firstStatus: 500, freshStatus: 200, wantResolves: 1, wantRequests: 1},
		{name: "accepted body must not restart", firstStatus: 200, shortBody: true, wantResolves: 1, wantRequests: 1},
		{name: "model failure must not loop", resolveError: true, wantResolves: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := resolvePlay
			t.Cleanup(func() { resolvePlay = old })
			var requests atomic.Int32
			data := []byte("clear-media-fixture")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				status := tc.firstStatus
				if r.URL.Path == "/fresh" {
					status = tc.freshStatus
				}
				if tc.shortBody {
					w.Header().Set("Content-Length", "1024")
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write(data)
				}
			}))
			defer srv.Close()
			resolves := 0
			resolvePlay = func(ctx context.Context, vid string, px int) (*hongguo.PlayInfo, error) {
				resolves++
				if vid != "123" || px != 720 {
					t.Errorf("refresh changed selection: vid=%s px=%d", vid, px)
				}
				if tc.resolveError {
					return nil, errors.New("model unavailable")
				}
				path := "/initial"
				if resolves > 1 {
					path = "/fresh"
				}
				return &hongguo.PlayInfo{VID: vid, URL: srv.URL + path, Quality: "720P", Width: 1280, Height: 720, Qualities: []int{1080, 720}}, nil
			}
			l := &library{dir: t.TempDir(), jobs: map[string]*job{}, quals: map[string][]int{}}
			l.limit.Store(1 << 20)
			j := l.ensure("123_720")
			select {
			case <-j.done:
			case <-time.After(5 * time.Second):
				t.Fatal("job did not stop within the recovery bound")
			}
			if resolves != tc.wantResolves || int(requests.Load()) != tc.wantRequests || (j.err == nil) != tc.wantOK {
				t.Fatalf("resolves=%d requests=%d err=%v", resolves, requests.Load(), j.err)
			}
			if tc.wantOK {
				got, err := os.ReadFile(l.path("123_720"))
				if err != nil || !bytes.Equal(got, data) || j.Quality() != "720P" || j.Phase() != "ready" {
					t.Fatalf("data=%q err=%v quality=%s phase=%s", got, err, j.Quality(), j.Phase())
				}
			} else if l.cached("123_720") || j.Phase() != "error" {
				t.Fatal("failed job was marked as cached/ready")
			}
			if _, err := os.Stat(l.partPath("123_720")); !os.IsNotExist(err) {
				t.Fatalf("job left a stale partial marker: %v", err)
			}
		})
	}
}

// Requests for an episode's danmaku at once share one load, which is kept.
func TestEpisodeDanmakuShared(t *testing.T) {
	old := fetchDanmaku
	t.Cleanup(func() { fetchDanmaku = old })
	var calls atomic.Int32
	release := make(chan struct{})
	fetchDanmaku = func(ctx context.Context, sid, vid string) ([]hongguo.Danmaku, error) {
		calls.Add(1)
		<-release
		return []hongguo.Danmaku{{Time: 1, Text: "hi", Color: "#FFFFFF"}}, nil
	}
	s := &mediaServer{}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := s.episodeDanmaku("1", "2"); err != nil || len(d) != 1 {
				t.Errorf("got %v %v", d, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if _, err := s.episodeDanmaku("1", "2"); err != nil || calls.Load() != 1 {
		t.Fatalf("calls = %d, err %v", calls.Load(), err)
	}
}
