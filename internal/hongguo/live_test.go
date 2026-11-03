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
	info, err := ResolvePlay(ctx, vid, 0)
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

// go test -run LiveSocial ./internal/hongguo -v  (with SMOVIE_LIVE=1)
func TestLiveSocial(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to hit the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const id = "7503745441910017049"
	info, err := Info(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("info: %.1f (%d) likes=%d favs=%d heat=%s first=%s cast=%d reviews=%d/%d %+v",
		info.Rating, info.RatingCount, info.Likes, info.Favorites, info.Heat, info.FirstShown.Format("2006-01-02"),
		len(info.Cast), len(info.Reviews.Items), info.Reviews.Total, info.Reviews.Items[0])
	d, err := Episodes(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := Episode(ctx, id, d.VIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("episode 2: likes=%d reviews=%d/%d", ep.Likes, len(ep.Reviews.Items), ep.Reviews.Total)
	if _, err := Episode(ctx, id, d.VIDs[40]); err != ErrLocked {
		t.Fatalf("episode 41: %v, want ErrLocked", err)
	}
}

// go test -run LiveDanmaku ./internal/hongguo -v  (with SMOVIE_LIVE=1)
func TestLiveDanmaku(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to hit the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := FetchDanmaku(ctx, "7701935931787316286", "7701937936731737112")
	if err != nil {
		t.Fatal(err)
	}
	if len(d) == 0 {
		t.Fatal("no danmaku")
	}
	t.Logf("danmaku: %d, first %.1fs %q, last %.1fs", len(d), d[0].Time, d[0].Text, d[len(d)-1].Time)
}

// go test -run LiveSeasons ./internal/hongguo -v  (with SMOVIE_LIVE=1)
func TestLiveSeasons(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to hit the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, id := range []string{"7701935931787316286", os.Getenv("SMOVIE_SERIES")} {
		if id == "" {
			continue
		}
		d, err := Episodes(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s album=%q", d.Title, d.AlbumID)
		if d.AlbumID == "" {
			continue
		}
		seasons, err := Seasons(ctx, d.Series, d.AlbumID)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range seasons {
			t.Logf("  第%d季 %s %s %s %s", s.Index, s.ID, s.Title, s.EpisodeLabel, s.Heat)
		}
	}
}
