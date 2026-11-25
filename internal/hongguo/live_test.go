package hongguo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

// go test -run LiveCatalog ./internal/hongguo -v  (with SMOVIE_LIVE=1)
func TestLiveCatalog(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to hit the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, cat := range Categories {
		p, err := Browse(ctx, cat.Slug, 1)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d genres", cat.Label, len(p.Genres))
		if len(p.Genres) == 0 {
			t.Fatal("no genres")
		}
		g, err := BrowseGenre(ctx, cat.Slug, p.Genres[0].Slug, 2)
		if err != nil || len(g.Items) == 0 {
			t.Fatalf("%s/%s: %v", cat.Slug, p.Genres[0].Slug, err)
		}
		t.Logf("  %s p2/%d: %s", p.Genres[0].Label, g.TotalPages, g.Items[0].Title)
	}
	for _, r := range Rankings {
		p, err := Rank(ctx, r.Slug, 2)
		if err != nil || len(p.Items) == 0 {
			t.Fatalf("%s: %v", r.Slug, err)
		}
		t.Logf("%s p2/%d: %s %s", r.Label, p.TotalPages, p.Items[0].Title, p.Items[0].Heat)
	}
}

// TestLiveContract exercises the full public + playback + decryption contract
// for scheduled CI drift detection. Run with SMOVIE_LIVE=1.
// Set SMOVIE_CONTRACT_OUT to write a redacted JSON summary (no URLs/IDs/keys).
func TestLiveContract(t *testing.T) {
	if os.Getenv("SMOVIE_LIVE") == "" {
		t.Skip("set SMOVIE_LIVE=1 to run the live contract check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	type contractSummary struct {
		CheckedAt      string   `json:"checked_at"`
		ProtocolVer    string   `json:"protocol_version"`
		BrowseCount    int      `json:"browse_count"`
		SearchCount    int      `json:"search_count"`
		EpisodeCount   int      `json:"episode_count"`
		Codec          string   `json:"codec"`
		Quality        string   `json:"quality"`
		Encrypted      bool     `json:"encrypted"`
		Qualities      []int    `json:"qualities"`
		DecodedFrameOK bool     `json:"decoded_frame_ok"`
		DanmakuCount   int      `json:"danmaku_count"`
		HotSearchCount int      `json:"hot_search_count"`
		RankCount      int      `json:"rank_count"`
		Errors         []string `json:"errors,omitempty"`
	}
	summary := contractSummary{
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		ProtocolVer: CurrentProtocolStatus().Version,
	}
	defer func() {
		outDir := os.Getenv("SMOVIE_CONTRACT_OUT")
		if outDir == "" {
			return
		}
		_ = os.MkdirAll(outDir, 0o755)
		b, _ := json.MarshalIndent(summary, "", "  ")
		_ = os.WriteFile(filepath.Join(outDir, "summary.json"), b, 0o644)
	}()

	p, err := Browse(ctx, Categories[0].Slug, 1)
	if err != nil || len(p.Items) == 0 {
		summary.Errors = append(summary.Errors, "browse")
		t.Fatalf("browse failed: %v", err)
	}
	summary.BrowseCount = len(p.Items)

	query := os.Getenv("SMOVIE_CONTRACT_QUERY")
	if query == "" {
		query = "重生"
	}
	s, err := Search(ctx, query, 1)
	if err != nil || len(s.Items) == 0 {
		summary.Errors = append(summary.Errors, "search")
		t.Fatalf("search failed: %v", err)
	}
	summary.SearchCount = len(s.Items)

	d, err := Episodes(ctx, s.Items[0].ID)
	if err != nil || len(d.VIDs) == 0 {
		summary.Errors = append(summary.Errors, "episodes")
		t.Fatalf("episodes failed: %v", err)
	}
	summary.EpisodeCount = len(d.VIDs)

	vid := d.VIDs[len(d.VIDs)-1]
	info, err := ResolvePlay(ctx, vid, 0)
	if err != nil {
		summary.Errors = append(summary.Errors, "resolve_play")
		t.Fatalf("resolve play failed: %v", err)
	}
	summary.Codec = info.Codec
	summary.Quality = info.Quality
	summary.Encrypted = info.ContentKey != ""
	summary.Qualities = info.Qualities

	dst := filepath.Join(t.TempDir(), "contract.mp4")
	if err := Download(ctx, info, dst, nil); err != nil {
		summary.Errors = append(summary.Errors, "download")
		t.Fatalf("download/decrypt failed: %v", err)
	}
	if ffmpegPath, lookErr := exec.LookPath("ffmpeg"); lookErr == nil {
		cmd := exec.CommandContext(ctx, ffmpegPath, "-v", "error", "-xerror", "-i", dst, "-frames:v", "1", "-f", "null", "-")
		if out, err := cmd.CombinedOutput(); err != nil {
			summary.Errors = append(summary.Errors, "ffmpeg_decode")
			t.Fatalf("ffmpeg frame decode failed: %v (%s)", err, string(out))
		}
		summary.DecodedFrameOK = true
	}

	if rankPage, err := Rank(ctx, Rankings[0].Slug, 1); err == nil {
		summary.RankCount = len(rankPage.Items)
	}
	if dm, err := FetchDanmaku(ctx, d.ID, d.VIDs[0]); err == nil {
		summary.DanmakuCount = len(dm)
	}
	if hot, err := HotSearch(ctx); err == nil {
		summary.HotSearchCount = len(hot)
	}
}
