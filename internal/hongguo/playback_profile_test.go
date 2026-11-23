package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolvePlayFullProfileRecoversEmptyModels(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	deviceRetry = time.Time{}
	registers, legacy, full := 0, 0, 0
	var identity string
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			registers++
			identity = fmt.Sprintf("12345678901234567%02d", registers)
			res := jsonResponse(fmt.Sprintf(`{"device_id_str":%q,"install_id_str":"2345678901234567890"}`, identity))
			res.Header.Add("Set-Cookie", "odin_tt=synthetic-issued-cookie; Path=/")
			return res, nil
		}
		if req.URL.Path != "/novel/player/multi_video_model/player/v1" && req.URL.Path != "/reading/bookapi/video_tab/multi_video_model/v1/" {
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
		}
		if !strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
			legacy++
			return jsonResponse(""), nil
		}
		full++
		if req.URL.Host != "api5-normal-sinfonlineb.fqnovel.com" || req.URL.Query().Get("device_id") != identity {
			t.Fatal("fallback changed the registered identity or endpoint")
		}
		if cookie, err := req.Cookie("odin_tt"); err != nil || cookie.Value != "synthetic-issued-cookie" {
			t.Fatal("registration cookie omitted")
		}
		return jsonResponse(`{"code":0,"data":{"1004":{"123":{"video_model":{"video_list":[{"codec_type":"h264","width":1920,"height":1080,"main_url":"https://example.fqnovelvod.com/full.mp4"}]}}}}}`), nil
	})}
	for i := 0; i < 2; i++ {
		info, err := ResolvePlay(context.Background(), "123", 0)
		if err != nil {
			t.Fatal(err)
		}
		if info.VID != "123" || info.Quality != "1080P" {
			t.Fatalf("wrong episode/quality: %+v", info)
		}
	}
	if registers != 2 || legacy != 4 || full != 2 {
		t.Fatalf("registers=%d legacy=%d full=%d; successful profile must be reused", registers, legacy, full)
	}
}

func TestFullMobileProfileBindsFinalBytes(t *testing.T) {
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic", Cookie: "odin_tt=issued; install_id=wrong; sessionid=account; store-region-src=uid"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 8; i++ {
		now := time.Unix(1800000000+int64(i), 0)
		req, err := signedFullMobileRequest(ctx, mobileVideoPayload("123"), dev, now)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != "POST" || req.URL.Scheme != "https" || req.URL.Host != "api5-normal-sinfonlineb.fqnovel.com" {
			t.Fatal("incorrect model route")
		}
		q := req.URL.Query()
		if q.Get("device_id") != dev.DeviceID || q.Get("iid") != dev.IID || q.Get("cdid") != dev.CDID || q.Get("device_type") != "25053RT47C" || q.Get("version_name") != "7.4.3.32" {
			t.Fatal("profile identity or registration description changed")
		}
		for name, want := range map[string]string{"install_id": dev.IID, "odin_tt": "issued", "store-region-src": "did"} {
			cookie, err := req.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Fatal("invalid playback cookie binding")
			}
		}
		if _, err := req.Cookie("sessionid"); err == nil {
			t.Fatal("account cookie forwarded")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		bh := md5.Sum(body)
		if req.Header.Get("x-ss-stub") != strings.ToUpper(hex.EncodeToString(bh[:])) {
			t.Fatal("stub not bound to transmitted gzip bytes")
		}
		if signingIV(signingIV(signingIV(0x20230928, sm3([]byte(req.URL.RawQuery))), bh[:]), le32(uint32(now.Unix())))&15 != 12 {
			t.Fatal("wrong digest branch")
		}
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Biz struct {
				Level int `json:"device_level"`
			} `json:"biz_param"`
			Mixed map[string][]string `json:"mixed_video_id_map"`
		}
		err = json.NewDecoder(zr).Decode(&payload)
		_ = zr.Close()
		if err != nil || payload.Biz.Level != 4 || len(payload.Mixed["1004"]) != 1 || payload.Mixed["1004"][0] != "123" {
			t.Fatal("fallback changed the selected episode")
		}
		if req.Header.Get("Content-Encoding") != "gzip" || req.Header.Get("x-khronos") != strconv.FormatInt(now.Unix(), 10) {
			t.Fatal("missing transmitted body metadata")
		}
		deadline, _ := ctx.Deadline()
		got, ok := req.Context().Deadline()
		if !ok || !got.Equal(deadline) {
			t.Fatal("fallback reset resolution deadline")
		}
	}
}

func TestFullMobileWireInteroperability(t *testing.T) {
	// Independent Python Gorgon implementation, hongguo-drama b20413b.
	if got := fullMobileGorgon("aid=8662&device_id=1234567890123456789", []byte("synthetic-body"), 1800000000, 0x1234); got != "84043412040564fb66647bde5c047a094448ddbb30b7e2e92729" {
		t.Fatalf("wire vector differs: %s", got)
	}
	// Hash-verified official SDK digest core, synthetic selector-12 inputs.
	for _, tc := range []struct {
		payload, digest string
		seconds         uint32
	}{
		{"db437dedd556eed4f5ec6b4be8474801920b9c861b004295db6c92f8d50cebefa9319333cffe44987bbec71216268f0f803d046b", "87ba47261c13d4f5d3fe117a9916072325e658d2", 1795440000},
		{"257c46fca774a9f37261717fbbef872c810f811cdff0a579466015c3154dde3e20187a4be1f87fbd288b546ed1169ddf87c5046b", "aae02c3e44402dbee5964b392da29dd1ad8a0f38", 1795474823},
		{"11c4aef4f0abb0117517df03815a21467566a67ac564209e4b04eee3cf553be516c47b55a277703fb647d16cfd8cd4958e4d056b", "256ad6470cbeed5ba1a9665bb34dea9ae6c9496c", 1795509646},
	} {
		payload, _ := hex.DecodeString(tc.payload)
		if hex.EncodeToString(hashF13Profile(payload[:32], payload[32:48], tc.seconds, true)) != tc.digest {
			t.Fatal("digest differs from SDK oracle")
		}
	}
}

func TestFullProfileFailuresAreBounded(t *testing.T) {
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	for _, tc := range []struct {
		name   string
		status int
		body   string
		cause  error
		cancel bool
	}{
		{name: "empty", status: 200}, {name: "forbidden", status: 403}, {name: "rate limited", status: 429}, {name: "server error", status: 503},
		{name: "business rejection", status: 200, body: `{"code":110001}`}, {name: "invalid JSON", status: 200, body: "invalid"},
		{name: "wrong episode", status: 200, body: `{"data":{"456":{"video_model":{}}}}`},
		{name: "network", cause: io.ErrUnexpectedEOF}, {name: "cancel", status: 200, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			registers, legacy, full := 0, 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
				}
				if !strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
					legacy++
					return jsonResponse(""), nil
				}
				full++
				if tc.cancel {
					cancel()
				}
				if tc.cause != nil {
					return nil, tc.cause
				}
				res := jsonResponse(tc.body)
				res.StatusCode = tc.status
				res.Header.Set("Retry-After", "60")
				return res, nil
			})}
			_, err := ResolvePlay(ctx, "123", 0)
			if err == nil || registers != 2 || legacy != 4 || full != 1 {
				t.Fatalf("registers=%d legacy=%d full=%d err=%v", registers, legacy, full, err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation")
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatal("lost network cause")
			}
			if strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "device_id=") {
				t.Fatal("authorized URL leaked")
			}
			if tc.status == 429 {
				var e *apiStatusError
				if !errors.As(err, &e) || !e.retryAt.After(time.Now().Add(50*time.Second)) {
					t.Fatal("lost service backoff")
				}
			}
		})
	}
}

func TestSuccessfulFullProfileHasBoundedRenewal(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, status := range []int{200, 403, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic", fullPlayback: true}
			registers, models := 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(`{"device_id_str":"3456789012345678901","install_id_str":"4567890123456789012"}`), nil
				}
				models++
				if req.URL.Path != "/novel/player/multi_video_model/player/v1" || !strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
					t.Fatal("full profile fell back to obsolete routes")
				}
				res := jsonResponse("")
				res.StatusCode = status
				return res, nil
			})}
			if _, err := ResolvePlay(context.Background(), "123", 0); err == nil {
				t.Fatal("rejection ignored")
			}
			wantModels, wantRegisters := 1, 0
			if status == 200 {
				wantModels, wantRegisters = 2, 1
			}
			if models != wantModels || registers != wantRegisters {
				t.Fatalf("models=%d registers=%d", models, registers)
			}
			if status == 200 && playbackDevice != nil {
				t.Fatal("exhausted identity remained cached")
			}
		})
	}
}

func TestFullProfileRejectsRedirectsAndRespectsDeadline(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	for _, redirect := range []bool{false, true} {
		t.Run(fmt.Sprint(redirect), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if redirect {
					res := jsonResponse("")
					res.StatusCode = 307
					res.Header.Set("Location", "https://attacker.test/collect")
					return res, nil
				}
				<-req.Context().Done()
				return nil, req.Context().Err()
			})}
			req, err := signedFullMobileRequest(ctx, mobileVideoPayload("123"), &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic"}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			_, err = fullMobileJSON(req)
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if !redirect && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost deadline")
			}
		})
	}
}

func TestConcurrentFullProfileReusesIdentity(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic", fullPlayback: true}
	calls := 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("device_id") != "1234567890123456789" || req.URL.Path != "/novel/player/multi_video_model/player/v1" || !strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
			t.Fatal("ready profile changed identity or route")
		}
		return jsonResponse(`{"data":{"123":{"video_model":{"video_list":[{"codec_type":"h264","width":1920,"height":1080,"main_url":"https://example.fqnovelvod.com/full.mp4"}]}}}}`), nil
	})}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ResolvePlay(context.Background(), "123", 0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 4 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestFullMobileJSONBoundsResponseSize(t *testing.T) {
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	httpClient = &http.Client{Transport: signingTransport(func(*http.Request) (*http.Response, error) { return jsonResponse(strings.Repeat("x", (4<<20)+10)), nil })}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, appAPIBase+"/novel/player/multi_video_model/player/v1", nil)
	if _, err := fullMobileJSON(req); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("oversized response accepted: %v", err)
	}
}
