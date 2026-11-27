package hongguo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beilunyang/openhongguo/sdk"
)

func htmlResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mockOpenhongguoRegister(req *sdk.Request) (*sdk.Response, bool) {
	if !strings.Contains(req.URL, "/service/2/device_register/") {
		return nil, false
	}
	return &sdk.Response{
		StatusCode: 200,
		Body:       []byte(`{"device_id_str":"9876543210123456789","install_id_str":"8765432109876543210"}`),
		Cookies:    map[string]string{"ttreq": "1$mock", "install_id": "8765432109876543210"},
	}, true
}

func TestEpisodesUsesOpenhongguoBatchFirst(t *testing.T) {
	isolatePlaybackSession(t)
	const sid = "7709153171259542553"
	ohgMu.Lock()
	ohgBackupEnabled = true
	ohgClient = nil
	ohgTransportOverride = funcSDKTransport(func(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
		if resp, ok := mockOpenhongguoRegister(req); ok {
			return resp, nil
		}
		if strings.Contains(req.URL, "/novel/player/multi_video_detail/player/v1") {
			return &sdk.Response{
				StatusCode: 200,
				Body: []byte(`{"code":0,"data":{"7709153171259542553":{"video_data":{
					"series_id_str":"7709153171259542553",
					"series_title":"测试短剧",
					"series_intro":"简介",
					"series_cover":"https://img.example.com/poster.heic",
					"episode_cnt":2,
					"episode_right_text":"全2集",
					"related_album_id":"7710935414118304830",
					"video_list":[
						{"vid":"7709153246937353278","vid_index":2,"title":"第2集"},
						{"vid":"7709153182684826686","vid_index":1,"title":"第1集"}
					]
				}}}}`),
			}, nil
		}
		t.Fatalf("unexpected openhongguo request: %s", req.URL)
		return nil, nil
	})
	ohgMu.Unlock()

	d, err := Episodes(context.Background(), sid)
	if err != nil {
		t.Fatalf("Episodes failed: %v", err)
	}
	if d.ID != sid || d.Title != "测试短剧" || d.AlbumID != "7710935414118304830" {
		t.Fatalf("unexpected metadata: %+v", d)
	}
	if d.Cover != "" {
		t.Fatalf("expected HEIC cover to be filtered out, got %q", d.Cover)
	}
	if len(d.VIDs) != 2 || d.VIDs[0] != "7709153182684826686" || d.VIDs[1] != "7709153246937353278" {
		t.Fatalf("episodes not sorted by number: %v", d.VIDs)
	}
}

func TestEpisodesFallsBackToOpenhongguoListEpisodes(t *testing.T) {
	isolatePlaybackSession(t)
	const sid = "7709153171259542553"
	ohgMu.Lock()
	ohgBackupEnabled = true
	ohgClient = nil
	ohgTransportOverride = funcSDKTransport(func(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
		if resp, ok := mockOpenhongguoRegister(req); ok {
			return resp, nil
		}
		if strings.Contains(req.URL, "/novel/player/multi_video_detail/player/v1") {
			return &sdk.Response{StatusCode: 200, Body: []byte(`{"code":0,"data":{}}`)}, nil
		}
		if strings.Contains(req.URL, "/novel/player/video_detail/v1/") {
			return &sdk.Response{
				StatusCode: 200,
				Body: []byte(`{"code":0,"data":{"video_data":{
					"series_id_str":"7709153171259542553",
					"series_title":"测试短剧",
					"episode_cnt":2,
					"video_list":[
						{"vid":"7709153182684826686","vid_index":1},
						{"vid":"7709153246937353278","vid_index":2}
					]
				}}}`),
			}, nil
		}
		t.Fatalf("unexpected openhongguo request: %s", req.URL)
		return nil, nil
	})
	ohgMu.Unlock()

	d, err := Episodes(context.Background(), sid)
	if err != nil {
		t.Fatalf("Episodes fallback to ListEpisodes failed: %v", err)
	}
	if len(d.VIDs) != 2 || d.VIDs[0] != "7709153182684826686" || d.VIDs[1] != "7709153246937353278" {
		t.Fatalf("unexpected vids: %v", d.VIDs)
	}
}

func isolateDeviceRetry(t *testing.T) {
	t.Helper()
	deviceMu.Lock()
	oldRetry, oldRetryErr := deviceRetry, deviceRetryErr
	deviceRetry, deviceRetryErr = time.Time{}, nil
	deviceMu.Unlock()
	t.Cleanup(func() {
		deviceMu.Lock()
		deviceRetry, deviceRetryErr = oldRetry, oldRetryErr
		deviceMu.Unlock()
	})
}

func TestEpisodesFallsBackToSignedAppVideoDetailAndPreserves64BitIDs(t *testing.T) {
	isolatePlaybackSession(t)
	isolateDeviceRetry(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })

	const sid = "7709153171259542553"
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		if req.URL.Path == videoDetailPath {
			// Use raw JSON numbers > 2^53 to verify UseNumber preserves exact digits.
			return jsonResponse(`{"code":0,"data":{"video_data":{
				"series_id":7709153171259542553,
				"series_title":"直连详情剧",
				"series_cover":"https://img.example.com/poster.jpg",
				"episode_cnt":2,
				"related_album_id":7710935414118304830,
				"video_list":[
					{"vid":7709153246937353279,"vid_index":2},
					{"vid":7709153182684826687,"vid_index":1}
				]
			}}}`), nil
		}
		return jsonResponse(""), nil
	})}

	d, err := Episodes(context.Background(), sid)
	if err != nil {
		t.Fatalf("Episodes signed video_detail/v1 fallback failed: %v", err)
	}
	if d.Title != "直连详情剧" || d.Cover != "https://img.example.com/poster.jpg" || d.AlbumID != "7710935414118304830" {
		t.Fatalf("unexpected metadata: %+v", d)
	}
	if len(d.VIDs) != 2 || d.VIDs[0] != "7709153182684826687" || d.VIDs[1] != "7709153246937353279" {
		t.Fatalf("64-bit IDs or ordering mismatch: %v", d.VIDs)
	}
}

func TestEpisodesFallsBackToSignedAppPreload(t *testing.T) {
	isolatePlaybackSession(t)
	isolateDeviceRetry(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })

	const sid = "7709153171259542553"
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		if req.URL.Path == videoDetailPath {
			return jsonResponse(""), nil
		}
		if req.URL.Path == multiVideoDetailPreloadPath {
			return jsonResponse(`{"code":0,"data":{"7709153171259542553":{"video_data":{
				"series_id_str":"7709153171259542553",
				"series_title":"预加载剧",
				"episode_cnt":2,
				"video_list":[
					{"video_id":"7709153182684826686","index":1},
					{"video_id":"7709153246937353278","index":2}
				]
			}}}}`), nil
		}
		return jsonResponse(""), nil
	})}

	d, err := Episodes(context.Background(), sid)
	if err != nil {
		t.Fatalf("Episodes preload fallback failed: %v", err)
	}
	if d.Title != "预加载剧" || len(d.VIDs) != 2 || d.VIDs[0] != "7709153182684826686" {
		t.Fatalf("unexpected preload detail: %+v", d)
	}
}

func TestEpisodesSiteFallbackRejectsObfuscatedAndTruncatedPreview(t *testing.T) {
	isolatePlaybackSession(t)
	isolateDeviceRetry(t)
	oldClient, oldSite := httpClient, siteClient
	t.Cleanup(func() { httpClient, siteClient = oldClient, oldSite })

	const sid = "7709153171259542553"
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(""), nil
	})}

	t.Run("obfuscated_v1_ids", func(t *testing.T) {
		siteClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
			return htmlResponse(`<script>window._ROUTER_DATA = {"loaderData":{"player_(series_id)/page":{"seriesDetail":{"series_id":"7709153171259542553","series_name":"测试","episode_cnt":150,"accessible_episode_cnt":5,"vid_list":["v1_Bwm-LAyZnV1YbDhHhYNnjQDek60"]}}}};</script>`), nil
		})}
		_, err := Episodes(context.Background(), sid)
		if err == nil || !strings.Contains(err.Error(), "混淆") {
			t.Fatalf("expected obfuscated vid error, got %v", err)
		}
	})

	t.Run("truncated_preview_only", func(t *testing.T) {
		siteClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
			return htmlResponse(`<script>window._ROUTER_DATA = {"loaderData":{"player_(series_id)/page":{"seriesDetail":{"series_id":"7709153171259542553","series_name":"测试","episode_cnt":80,"accessible_episode_cnt":3,"vid_list":["101","102","103"]}}}};</script>`), nil
		})}
		_, err := Episodes(context.Background(), sid)
		if err == nil || !strings.Contains(err.Error(), "试看分集") {
			t.Fatalf("expected truncated preview error, got %v", err)
		}
	})

	t.Run("valid_full_site_list", func(t *testing.T) {
		siteClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
			return htmlResponse(`<script>window._ROUTER_DATA = {"loaderData":{"player_(series_id)/page":{"seriesDetail":{"series_id":"7709153171259542553","series_name":"完整剧","episode_cnt":3,"accessible_episode_cnt":3,"vid_list":["101","102","103"]}}}};</script>`), nil
		})}
		d, err := Episodes(context.Background(), sid)
		if err != nil {
			t.Fatalf("expected valid site fallback to succeed, got %v", err)
		}
		if len(d.VIDs) != 3 || d.VIDs[2] != "103" {
			t.Fatalf("unexpected site vids: %v", d.VIDs)
		}
	})
}

func TestEpisodeSocialReturnsErrLockedForLayoutOnlyPage(t *testing.T) {
	oldSite := siteClient
	t.Cleanup(func() { siteClient = oldSite })

	siteClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		return htmlResponse(`<script>window._ROUTER_DATA = {"loaderData":{"player_layout":{},"player_(series_id)/(vid)/layout":{}}};</script>`), nil
	})}

	_, err := Episode(context.Background(), "7503745441910017049", "7503780574541122622")
	if err != ErrLocked {
		t.Fatalf("expected ErrLocked for layout-only numeric vid page, got %v", err)
	}
}
