package hongguo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// go test -run Live ./internal/hongguo -v  (with SMOVIE_LIVE=1)
func TestLive(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to hit the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Browse(ctx, "real-drama", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("browse: %d items, %d pages, first=%+v", len(p.Items), p.TotalPages, p.Items[0].Title)
	s, err := Search(ctx, "重生", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("search: %d items, first=%s %s", len(s.Items), s.Items[0].ID, s.Items[0].Title)
	d, err := Episodes(ctx, s.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("episodes: %s %d vids", d.Title, len(d.VIDs))
	vid := d.VIDs[len(d.VIDs)-1] // the last one, beyond the website's free range
	info, err := ResolvePlay(ctx, vid)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("play: codec=%s q=%s key=%v url=%.80s", info.Codec, info.Quality, info.ContentKey != "", info.URL)
	if os.Getenv("SMOVIE_DL") != "" {
		dst := filepath.Join(t.TempDir(), vid+".mp4")
		if err := Download(ctx, info, dst, nil); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(dst)
		t.Logf("downloaded %d bytes -> %s", st.Size(), dst)
		if out := os.Getenv("SMOVIE_DL"); out != "1" {
			b, _ := os.ReadFile(dst)
			_ = os.WriteFile(out, b, 0o644)
		}
	}
}
