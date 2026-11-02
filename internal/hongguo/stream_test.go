package hongguo

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testKey = "00112233445566778899aabbccddeeff"
	testKID = "0f0e0d0c0b0a09080706050403020100"
)

// packets lists a file's packets with their hashes, as ffmpeg reads them.
func packets(t *testing.T, ff, file string, key string) string {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error"}
	if key != "" {
		args = append(args, "-decryption_key", key)
	}
	args = append(args, "-i", file, "-map", "0", "-c", "copy", "-f", "framemd5", "-")
	out, err := exec.Command(ff, args...).Output()
	if err != nil {
		t.Fatalf("ffmpeg %s: %v", file, err)
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// checkDownload downloads src from a local server and checks that the
// result has src's packets, decrypted, and whether it streamed, with each
// part told ready already final.
func checkDownload(t *testing.T, ff, src, key string, wantStream bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, src)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.mp4")
	type snap struct {
		ready int64
		data  []byte
	}
	var snaps []snap
	var streamed bool
	var last int64
	err := Download(context.Background(), &PlayInfo{URL: srv.URL, ContentKey: key, Codec: "hevc"}, dst, func(p Progress) {
		if !p.Stream {
			return
		}
		streamed = true
		if p.Ready < last {
			t.Errorf("ready went back from %d to %d", last, p.Ready)
		}
		last = p.Ready
		b, _ := os.ReadFile(dst)
		if int64(len(b)) < p.Ready {
			t.Fatalf("ready %d but %d bytes written", p.Ready, len(b))
		}
		snaps = append(snaps, snap{p.Ready, b[:p.Ready]})
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed != wantStream {
		t.Fatalf("streamed = %v, want %v", streamed, wantStream)
	}
	final, _ := os.ReadFile(dst)
	for _, s := range snaps {
		if !bytes.Equal(final[:s.ready], s.data) {
			t.Fatalf("bytes before %d changed after told ready", s.ready)
		}
	}
	if wantStream {
		st, _ := os.Stat(src)
		if last != st.Size() || int64(len(final)) != st.Size() {
			t.Fatalf("ready %d, size %d, want %d", last, len(final), st.Size())
		}
	}
	if got, want := packets(t, ff, dst, ""), packets(t, ff, src, key); got != want {
		t.Fatalf("packets differ:\n got %.400s\nwant %.400s", got, want)
	}
}

func TestDownloadDecrypts(t *testing.T) {
	ff := FFmpeg()
	if ff == "" {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	gen := func(name, codec string, enc bool, flags ...string) string {
		out := filepath.Join(dir, name)
		args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440", "-t", "2", "-c:v", codec, "-c:a", "aac"}
		if codec == "libx265" {
			args = append(args, "-tag:v", "hvc1", "-x265-params", "log-level=none")
		}
		if enc {
			args = append(args, "-encryption_scheme", "cenc-aes-ctr", "-encryption_key", testKey, "-encryption_kid", testKID)
		}
		args = append(args, flags...)
		if err := exec.Command(ff, append(args, out)...).Run(); err != nil {
			t.Skipf("ffmpeg cannot make %s: %v", name, err)
		}
		return out
	}
	t.Run("h264 index first", func(t *testing.T) {
		checkDownload(t, ff, gen("a.mp4", "libx264", true, "-movflags", "+faststart"), testKey, true)
	})
	t.Run("hevc index first", func(t *testing.T) {
		checkDownload(t, ff, gen("b.mp4", "libx265", true, "-movflags", "+faststart"), testKey, true)
	})
	t.Run("index last", func(t *testing.T) {
		checkDownload(t, ff, gen("c.mp4", "libx265", true), testKey, false)
	})
	t.Run("clear", func(t *testing.T) {
		checkDownload(t, ff, gen("d.mp4", "libx264", false, "-movflags", "+faststart"), "", true)
	})
}

// go test -run DownloadLive ./internal/hongguo  (with SMOVIE_RAW=dir of
// <vid>.enc files and SMOVIE_KEYS=vid:key,…) checks real episodes.
func TestDownloadLive(t *testing.T) {
	dir, keys := os.Getenv("SMOVIE_RAW"), os.Getenv("SMOVIE_KEYS")
	ff := FFmpeg()
	if dir == "" || keys == "" || ff == "" {
		t.Skip("set SMOVIE_RAW and SMOVIE_KEYS")
	}
	for _, kv := range strings.Split(keys, ",") {
		vid, key, _ := strings.Cut(kv, ":")
		t.Run(vid, func(t *testing.T) {
			checkDownload(t, ff, filepath.Join(dir, vid+".enc"), key, true)
		})
	}
}
