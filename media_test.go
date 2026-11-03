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
