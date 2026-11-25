package hongguo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beilunyang/openhongguo/sdk"
	"github.com/beilunyang/openhongguo/sdk/model"
)

type funcSDKTransport func(ctx context.Context, req *sdk.Request) (*sdk.Response, error)

func (f funcSDKTransport) Do(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
	return f(ctx, req)
}

func TestSelectOpenhongguoStreamAndDASH(t *testing.T) {
	pb := &model.Playback{
		EpisodeID: "123",
		Protected: false,
		AudioURL:  "https://audio.fqnovelvod.com/track.m4a",
		Streams: []model.Stream{
			{Definition: "1080p", Codec: "bytevc2", Width: 1920, Height: 1080, URL: "https://example.fqnovelvod.com/v2.mp4"},
			{Definition: "1080p", Codec: "bytevc1", Width: 1920, Height: 1080, URL: "https://example.fqnovelvod.com/1080.mp4", BackupURL: "https://backup.fqnovelvod.com/1080.mp4"},
			{Definition: "720p", Codec: "h264", Width: 1280, Height: 720, URL: "https://example.fqnovelvod.com/720.mp4"},
		},
	}
	info, err := selectOpenhongguoStream("123", pb, "00112233445566778899aabbccddeeff", 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Codec != "hevc" || info.Quality != "1080P" || info.URL != "https://example.fqnovelvod.com/1080.mp4" {
		t.Fatalf("unexpected selection: %+v", info)
	}
	if info.AudioURL != "https://audio.fqnovelvod.com/track.m4a" {
		t.Fatalf("DASH audio URL lost: %q", info.AudioURL)
	}
	if len(info.BackupURLs) != 1 || info.BackupURLs[0] != "https://backup.fqnovelvod.com/1080.mp4" {
		t.Fatalf("backup URL lost: %v", info.BackupURLs)
	}
	low, err := selectOpenhongguoStream("123", pb, "", 720)
	if err != nil || low.Quality != "720P" || low.Codec != "h264" {
		t.Fatalf("720p cap failed: %+v err=%v", low, err)
	}
}

func TestResolvePlayFallsBackToOpenhongguoOnEmptyResponses(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}

	// Built-in client returns empty 200 on all playback routes.
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		return jsonResponse(""), nil
	})}

	// Enable openhongguo backup with a mock sdk.Transport.
	const vid = "7700000000000000123"
	ohgCalls := 0
	p := *currentProfile.Load()
	p.raw.Playback.RouteOrder = []string{"v5_post", "v5_tab", "v5_full", "openhongguo"}
	p.routeSet = map[string]bool{"v5_post": true, "v5_tab": true, "v5_full": true, "openhongguo": true}
	currentProfile.Store(&p)
	ohgMu.Lock()
	ohgBackupEnabled = true
	ohgClient = nil
	ohgTransportOverride = funcSDKTransport(func(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
		if strings.Contains(req.URL, "/service/2/device_register/") {
			return &sdk.Response{
				StatusCode: 200,
				Body:       []byte(`{"device_id_str":"9876543210123456789","install_id_str":"8765432109876543210"}`),
				Cookies:    map[string]string{"ttreq": "1$mock", "install_id": "8765432109876543210"},
			}, nil
		}
		ohgCalls++
		return &sdk.Response{
			StatusCode: 200,
			Body: []byte(`{"code":0,"data":{"1004":{"7700000000000000123":{"video_model":{"video_id":"v123","video_duration":60,"video_list":[{"codec_type":"bytevc1","vwidth":1080,"vheight":1920,"main_url":"https://example.fqnovelvod.com/ohg.mp4"}]}}}}}`),
		}, nil
	})
	ohgMu.Unlock()

	info, err := ResolvePlay(context.Background(), vid, 0)
	if err != nil {
		t.Fatalf("expected openhongguo backup recovery, got %v", err)
	}
	if ohgCalls == 0 || info.URL != "https://example.fqnovelvod.com/ohg.mp4" || info.Codec != "hevc" {
		t.Fatalf("unexpected backup result: calls=%d info=%+v", ohgCalls, info)
	}
}

func TestResolvePlayParsesDASHModel(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic"}
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"code":0,"data":{"123":{"video_model":{
			"video_id":"dash1","video_duration":1800,
			"dynamic_video":{
				"dynamic_video_list":[{"codec_type":"h264","vwidth":1920,"vheight":1080,"main_url":"https://example.fqnovelvod.com/movie.m4v"}],
				"dynamic_audio_list":[{"main_url":"https://example.fqnovelvod.com/movie.m4a"}]
			}
		}}}}`), nil
	})}

	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatalf("DASH model failed: %v", err)
	}
	if info.URL != "https://example.fqnovelvod.com/movie.m4v" || info.AudioURL != "https://example.fqnovelvod.com/movie.m4a" {
		t.Fatalf("unexpected DASH PlayInfo: %+v", info)
	}
}

func TestSeasonsUsesOpenhongguoAlbumFirst(t *testing.T) {
	isolatePlaybackSession(t)
	ohgMu.Lock()
	ohgBackupEnabled = true
	ohgClient = nil
	ohgTransportOverride = funcSDKTransport(func(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
		if strings.Contains(req.URL, "/service/2/device_register/") {
			return &sdk.Response{
				StatusCode: 200,
				Body:       []byte(`{"device_id_str":"9876543210123456789","install_id_str":"8765432109876543210"}`),
				Cookies:    map[string]string{"ttreq": "1$mock", "install_id": "8765432109876543210"},
			}, nil
		}
		return &sdk.Response{
			StatusCode: 200,
			Body: []byte(`{"code":0,"data":{"album_data":{"album_id":"7500000000000000001","album_title":"测试系列","season_cnt":2,"series_list":[
				{"series_id":"7500000000000000012","series_title":"测试系列第二季","season_index":2,"episode_cnt":60,"series_cover":"https://img.example.com/s2.jpg"},
				{"series_id":"7500000000000000011","series_title":"测试系列第一季","season_index":1,"episode_cnt":50,"series_cover":"https://img.example.com/s1.heic"}
			]}}}`),
		}, nil
	})
	ohgMu.Unlock()

	seed := Series{ID: "7500000000000000011", Title: "测试系列第一季", Cover: "https://web.example.com/s1.webp"}
	gotProgress := 0
	seasons, err := SeasonsProgress(context.Background(), seed, "7500000000000000001", func(part []Season) {
		gotProgress = len(part)
	})
	if err != nil {
		t.Fatalf("SeasonsProgress failed: %v", err)
	}
	if len(seasons) != 2 || gotProgress != 2 {
		t.Fatalf("expected 2 seasons, got %+v (progress=%d)", seasons, gotProgress)
	}
	if seasons[0].Index != 1 || seasons[0].Cover != "https://web.example.com/s1.webp" {
		t.Fatalf("expected season 1 first with seed web cover fallback, got %+v", seasons[0])
	}
	if seasons[1].Index != 2 || seasons[1].Cover != "https://img.example.com/s2.jpg" {
		t.Fatalf("expected season 2 second, got %+v", seasons[1])
	}
}

func TestRemoteSignerConfigAndHeaderReplacement(t *testing.T) {
	isolatePlaybackSession(t)
	if err := SetSignerConfig("http://signer.example.com", ""); err == nil {
		t.Fatal("non-loopback HTTP signer URL should be rejected")
	}
	if err := SetSignerConfig("https://signer.example.com", "tok"); err != nil {
		t.Fatalf("HTTPS signer URL rejected: %v", err)
	}

	var gotToken, gotURL string
	signerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feature-hash" {
			http.NotFound(w, r)
			return
		}
		gotToken = r.Header.Get("X-Signer-Token")
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		gotURL = in["url"]
		_, _ = w.Write([]byte(`{"headers":{"X-Medusa":"remote-medusa","X-Gorgon":"remote-gorgon","X-Khronos":"1800000000","X-Helios":"remote-helios"}}`))
	}))
	defer signerSrv.Close()

	oldClient := httpClient
	httpClient = signerSrv.Client()
	t.Cleanup(func() { httpClient = oldClient })

	if err := SetSignerConfig(signerSrv.URL, "secret-token"); err != nil {
		t.Fatalf("loopback HTTP signer URL rejected: %v", err)
	}
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic"}
	req, err := signedFullMobileRequest(context.Background(), mobileVideoPayload("123"), dev, time.Unix(1800000000, 0))
	if err != nil {
		t.Fatalf("signedFullMobileRequest with remote signer failed: %v", err)
	}
	if gotToken != "secret-token" || !strings.Contains(gotURL, "/novel/player/multi_video_model/player/v1") {
		t.Fatalf("signer did not receive expected request: token=%q url=%q", gotToken, gotURL)
	}
	if req.Header.Get("x-medusa") != "remote-medusa" || req.Header.Get("x-gorgon") != "remote-gorgon" {
		t.Fatalf("headers not replaced by remote signer: medusa=%q gorgon=%q", req.Header.Get("x-medusa"), req.Header.Get("x-gorgon"))
	}
}
