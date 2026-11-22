package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A nonempty video_tab model can contain only ByteVC2. Its own video-info
// endpoint can still negotiate HEVC for exactly the same content and duration.
func TestResolvePlayNegotiatesHEVCFromByteVC2Model(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	deviceRetry = time.Time{}
	registers, posts, tabs, infos := 0, 0, 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/service/2/device_register/":
			registers++
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		case "/novel/player/multi_video_model/player/v1":
			posts++
			return jsonResponse(""), nil
		case "/reading/bookapi/video_tab/multi_video_model/v1/":
			tabs++
			return jsonResponse(`{"code":0,"data":{"video_model_data":{"123":{"video_model":{
				"video_id":"same-content","video_duration":47.2,
				"fallback_api":"https://vas-lf-x.snssdk.com/video/fplay/same-content?codec_type=4&ptoken=synthetic&force_fids=synthetic",
				"video_list":[{"video_meta":{"codec_type":"bytevc2","vwidth":1920,"vheight":1080},"main_url":"https://example.fqnovelvod.com/unsupported.mp4"}]
			}}}}}`), nil
		case "/video/fplay/same-content":
			infos++
			if req.URL.Hostname() != "vas-lf-x.snssdk.com" || req.URL.Query().Get("codec_type") != "1" {
				t.Fatal("missing HEVC negotiation")
			}
			if req.URL.Query().Get("ptoken") != "synthetic" || req.URL.Query().Get("force_fids") != "synthetic" {
				t.Fatal("negotiation changed content authorization")
			}
			if req.Header.Get("Cookie") != "" || req.Header.Get("x-medusa") != "" {
				t.Fatal("video-info request carries model credentials")
			}
			return jsonResponse(`{"code":0,"video_info":{"code":0,"data":{
				"video_id":"same-content","video_duration":47.2,
				"video_list":[{"codec_type":"h265","width":1920,"height":1080,"main_url":"https://example.fqnovelvod.com/1080.mp4","backup_url":"https://backup.fqnovelvod.com/1080.mp4"},
				{"codec_type":"h264","width":1280,"height":720,"main_url":"https://example.fqnovelvod.com/720.mp4"}]
			}}}`), nil
		default:
			t.Fatalf("unexpected request %s", req.URL.Path)
			return nil, nil
		}
	})}
	for _, tc := range []struct {
		max          int
		quality, url string
	}{
		{0, "1080P", "https://example.fqnovelvod.com/1080.mp4"},
		{720, "720P", "https://example.fqnovelvod.com/720.mp4"},
	} {
		info, err := ResolvePlay(context.Background(), "123", tc.max)
		if err != nil {
			t.Fatal(err)
		}
		if info.VID != "123" || info.Quality != tc.quality || info.URL != tc.url {
			t.Fatalf("wrong media: %+v", info)
		}
		if fmt.Sprint(info.Qualities) != "[1080 720]" {
			t.Fatalf("wrong qualities %v", info.Qualities)
		}
		if tc.max == 0 && strings.Join(info.BackupURLs, ",") != "https://backup.fqnovelvod.com/1080.mp4" {
			t.Fatal("lost same-rendition backup")
		}
	}
	if registers != 1 || posts != 2 || tabs != 2 || infos != 2 {
		t.Fatalf("registers=%d posts=%d tabs=%d infos=%d", registers, posts, tabs, infos)
	}
}

func TestCodecNegotiationRejectsOtherContentAndInvalidResponses(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	for _, tc := range []struct {
		name, sourceID, sourceDuration, responseID, responseDuration, code, nestedCode, codec string
		wantInfoCalls                                                                         int
	}{
		{name: "different content", responseID: "other", responseDuration: "47.2", wantInfoCalls: 1},
		{name: "missing response ID", responseDuration: "47.2", wantInfoCalls: 1},
		{name: "preview duration", responseID: "same", responseDuration: "10", wantInfoCalls: 1},
		{name: "missing response duration", responseID: "same", responseDuration: "null", wantInfoCalls: 1},
		{name: "nonfinite response duration", responseID: "same", responseDuration: `"NaN"`, wantInfoCalls: 1},
		{name: "infinite response duration", responseID: "same", responseDuration: `"+Inf"`, wantInfoCalls: 1},
		{name: "missing source ID", sourceID: " ", wantInfoCalls: 0},
		{name: "missing source duration", sourceDuration: "null", wantInfoCalls: 0},
		{name: "nonfinite source duration", sourceDuration: `"NaN"`, wantInfoCalls: 0},
		{name: "business rejection", responseID: "same", responseDuration: "47.2", code: "110001", wantInfoCalls: 1},
		{name: "nested business rejection", responseID: "same", responseDuration: "47.2", nestedCode: "1001", wantInfoCalls: 1},
		{name: "still ByteVC2", responseID: "same", responseDuration: "47.2", codec: "bytevc2", wantInfoCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatePlaybackSession(t)
			playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
			calls, infoCalls := 0, 0
			id, duration := tc.sourceID, tc.sourceDuration
			if id == "" {
				id = "same"
			}
			if duration == "" {
				duration = "47.2"
			}
			code, nestedCode, codec := tc.code, tc.nestedCode, tc.codec
			if code == "" {
				code = "0"
			}
			if nestedCode == "" {
				nestedCode = "0"
			}
			if codec == "" {
				codec = "h265"
			}
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path == "/novel/player/multi_video_model/player/v1" {
					return jsonResponse(fmt.Sprintf(`{"data":{"123":{"video_model":{"video_id":%q,"video_duration":%s,"fallback_api":"https://vas-lf-x.snssdk.com/video/fplay/same?codec_type=4","video_list":[{"codec_type":"bytevc2"}]}}}}`, id, duration)), nil
				}
				infoCalls++
				if req.URL.Hostname() != "vas-lf-x.snssdk.com" {
					t.Fatal("unexpected endpoint")
				}
				responseDuration := tc.responseDuration
				if responseDuration == "" {
					responseDuration = "47.2"
				}
				return jsonResponse(fmt.Sprintf(`{"code":%s,"video_info":{"code":%s,"data":{"video_id":%q,"video_duration":%s,"video_list":[{"codec_type":%q,"main_url":"https://example.fqnovelvod.com/video.mp4","width":1920,"height":1080}]}}}`, code, nestedCode, tc.responseID, responseDuration, codec)), nil
			})}
			if _, err := ResolvePlay(context.Background(), "123", 0); err == nil {
				t.Fatal("unverified or incompatible content accepted")
			}
			if infoCalls != tc.wantInfoCalls || calls != 1+tc.wantInfoCalls {
				t.Fatalf("calls=%d info=%d; negotiation must be bounded", calls, infoCalls)
			}
		})
	}
}

func TestCodecNegotiationURLBoundaries(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	for _, raw := range []string{
		"http://vas-lf-x.snssdk.com/video/fplay/test", "https://127.0.0.1/private", "https://snssdk.com.attacker.test/video",
		"https://user:pass@vas-lf-x.snssdk.com/video", "https://vas-lf-x.snssdk.com:8443/video", "https://vas-lf-x.snssdk.com/video#fragment",
		"https://evil-snssdk.com/video", "file:///private/file", "://invalid",
	} {
		t.Run(raw, func(t *testing.T) {
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) { calls++; return jsonResponse(`{}`), nil })}
			encoded, _ := json.Marshal(raw)
			model := &videoInfo{VideoID: "same", Duration: 47.2, FallbackAPI: encoded}
			if _, err := loadFallbackVideoInfo(context.Background(), model, true); err == nil {
				t.Fatal("untrusted video-info URL accepted")
			}
			if calls != 0 {
				t.Fatal("untrusted endpoint was requested")
			}
		})
	}
	for _, location := range []string{"https://127.0.0.1/private", "http://vas-lf-x.snssdk.com/video", "https://attacker.test/video"} {
		t.Run("redirect "+location, func(t *testing.T) {
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				res := jsonResponse("")
				res.StatusCode = 302
				res.Header.Set("Location", location)
				return res, nil
			})}
			model := &videoInfo{VideoID: "same", Duration: 47.2, FallbackAPI: json.RawMessage(`"https://vas-lf-x.snssdk.com/video"`)}
			if _, err := loadFallbackVideoInfo(context.Background(), model, true); err == nil {
				t.Fatal("untrusted redirect accepted")
			}
			if calls != 1 {
				t.Fatal("untrusted redirect was followed")
			}
		})
	}
}

func TestCodecNegotiationFailuresAreBounded(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
		cancel bool
	}{
		{name: "empty", status: 200}, {name: "malformed", status: 200, body: "invalid"},
		{name: "forbidden", status: 403}, {name: "rate limited", status: 429}, {name: "server error", status: 503},
		{name: "network", err: io.ErrUnexpectedEOF}, {name: "canceled", status: 200, cancel: true, body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.cancel {
					cancel()
				}
				if tc.err != nil {
					return nil, tc.err
				}
				res := jsonResponse(tc.body)
				res.StatusCode = tc.status
				return res, nil
			})}
			model := &videoInfo{VideoID: "same", Duration: 47.2, FallbackAPI: json.RawMessage(`"https://vas-lf-x.snssdk.com/video"`)}
			_, err := loadFallbackVideoInfo(ctx, model, true)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost original error: %v", err)
			}
			if strings.Contains(err.Error(), "snssdk.com") {
				t.Fatal("failure leaked the authorized video-info URL")
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d; video-info must not loop", calls)
			}
		})
	}
}

func TestPlayableInlineModelDoesNotNegotiate(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	for _, codec := range []string{"h264", "hevc", "bytevc1"} {
		t.Run(codec, func(t *testing.T) {
			isolatePlaybackSession(t)
			playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return jsonResponse(fmt.Sprintf(`{"data":{"123":{"video_model":{"fallback_api":"https://attacker.test/unused","video_list":[{"codec_type":%q,"width":1920,"height":1080,"main_url":"https://example.fqnovelvod.com/video.mp4"}]}}}}`, codec)), nil
			})}
			if _, err := ResolvePlay(context.Background(), "123", 0); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("working model triggered fallback")
			}
		})
	}
}

func TestVideoInfoOnlyModelKeepsOriginalCodecRequest(t *testing.T) {
	isolatePlaybackSession(t)
	playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	calls := 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonResponse(`{"data":{"123":{"video_model":{"fallback_api":"https://vas-lf-x.snssdk.com/video?codec_type=0&ptoken=synthetic"}}}}`), nil
		}
		if req.URL.Query().Get("codec_type") != "0" {
			t.Fatal("video-info-only model unexpectedly changed codec")
		}
		return jsonResponse(`{"video_info":{"data":{"video_list":[{"codec_type":"h264","main_url":"https://example.fqnovelvod.com/video.mp4","width":1920,"height":1080}]}}}`), nil
	})}
	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || info.Codec != "h264" {
		t.Fatalf("calls=%d info=%+v", calls, info)
	}
}

func TestCodecNegotiationSharesResolutionDeadline(t *testing.T) {
	isolatePlaybackSession(t)
	playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	calls := 0
	var modelDeadline time.Time
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := req.Context().Deadline()
		if !ok {
			t.Fatal("unbounded resolution request")
		}
		if calls == 1 {
			modelDeadline = deadline
			if time.Until(deadline) > 20*time.Second {
				t.Fatal("resolution exceeds total budget")
			}
			return jsonResponse(`{"data":{"123":{"video_model":{"video_id":"same","video_duration":47.2,"fallback_api":"https://vas-lf-x.snssdk.com/video","video_list":[{"codec_type":"bytevc2"}]}}}}`), nil
		}
		if !deadline.Equal(modelDeadline) {
			t.Fatal("fallback reset the resolution deadline")
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := ResolvePlay(ctx, "123", 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
