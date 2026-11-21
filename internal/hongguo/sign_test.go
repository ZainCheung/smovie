package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Independent 551 vectors include a zero nonce, empty query/body, and a nonce
// above MaxInt32. The former timestamp placeholders cannot satisfy these.
func TestPlayback551Vectors(t *testing.T) {
	b, err := os.ReadFile("testdata/playback-551.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Query, Body, Argus, Ladon string
		TS, Nonce                 uint32
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, v := range vectors {
		if got := playbackArgus(v.Query, []byte(v.Body), v.TS, v.Nonce); got != v.Argus {
			t.Errorf("vector %d: Argus differs from reference", i)
		}
		q, _ := url.ParseQuery(v.Query)
		if got := playbackLadon(v.TS, firstNonEmpty(q.Get("aid"), "1967"), 0x04030201); got != v.Ladon {
			t.Errorf("vector %d: Ladon differs from reference", i)
		}
		for _, got := range []string{
			playbackArgus(v.Query+"&video_id=changed", []byte(v.Body), v.TS, v.Nonce),
			playbackArgus(v.Query, []byte(v.Body+" "), v.TS, v.Nonce),
			playbackArgus(v.Query, []byte(v.Body), v.TS+1, v.Nonce),
			playbackArgus(v.Query, []byte(v.Body), v.TS, v.Nonce^1),
		} {
			if got == v.Argus {
				t.Fatalf("vector %d: Argus did not bind its inputs", i)
			}
		}
	}
}

func TestPlaybackModelRequestSignature(t *testing.T) {
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
	for _, api := range []string{videoAPI, "https://api5-normal-hl.fqnovel.com/novel/player/multi_video_model/v1/", detailAPI, tabVideoAPI} {
		t.Run(api, func(t *testing.T) {
			req, err := signedRequest(context.Background(), api, []byte(`{"video_id":"123"}`), dev, time.Unix(1800000000, 0))
			if err != nil {
				t.Fatal(err)
			}
			argus, _ := base64.StdEncoding.DecodeString(req.Header.Get("X-Argus"))
			ladon, _ := base64.StdEncoding.DecodeString(req.Header.Get("X-Ladon"))
			if req.URL.Path == "/novel/player/multi_video_model/v1/" {
				if len(argus) < 64 || !bytes.HasPrefix(argus, []byte{0xf2, 0x81}) {
					t.Error("playback request lacks full Argus 551")
				}
				if len(ladon) != 36 {
					t.Error("playback request lacks full Ladon")
				}
				if req.Header.Get("X-Medusa") != "" {
					t.Error("playback sends a conflicting Medusa envelope")
				}
			} else {
				if req.Header.Get("X-Medusa") == "" || len(argus) != 4 || len(ladon) != 4 {
					t.Error("unrelated API signing profile changed")
				}
			}
		})
	}
}

func TestSM3(t *testing.T) {
	for _, v := range []struct{ input, want string }{
		{"abc", "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"},
		{string(bytes.Repeat([]byte("abcd"), 16)), "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"},
	} {
		if got := hex.EncodeToString(sm3([]byte(v.input))); got != v.want {
			t.Fatalf("SM3 = %s, want %s", got, v.want)
		}
	}
}

// Reference outputs from hongguo-drama's app-source.js using fixed time,
// process identity and random numbers; these cover all six branch-0 variants
// and all four Medusa substitution tables. No Node runtime is needed by tests.
func TestSigningReferenceVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/signing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		TS                                          uint32
		Query, Gorgon, Helios, F13, Medusa, Version string
		Body                                        *string
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, v := range vectors {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var body []byte
			var bh [16]byte
			if v.Body != nil {
				body = []byte(*v.Body)
				bh = md5.Sum(body)
			}
			qh := sm3([]byte(v.Query))
			if branch := signingBranch(qh, bh[:], v.TS); branch != 0 {
				t.Fatalf("fixture uses branch %d", branch)
			}
			if got := xGorgon(v.Query, body, v.TS, 0x1234); got != v.Gorgon {
				t.Errorf("Gorgon = %s, want %s", got, v.Gorgon)
			}
			if got := helios(v.TS, 0x87654321); got != v.Helios {
				t.Errorf("Helios = %s, want %s", got, v.Helios)
			}
			if got := hex.EncodeToString(hashF13(qh, bh[:], v.TS)); got != v.F13 {
				t.Errorf("F13 = %s, want %s", got, v.F13)
			}
			step := 0
			randoms := []uint32{0x12345678, 0x9abcdef0, 0x10203040, 0x55667788, 0xabcdef01}
			next := func() uint32 { v := randoms[step%len(randoms)]; step++; return v }
			version := v.Version
			if version == "" {
				version = "7.1.3.32"
			}
			got := buildMedusa(v.Query, body, v.TS, &device{DeviceID: "1234567890123456789"}, "fixed-signing-process-nonce", 1793606400123, next, version)
			if got != v.Medusa {
				t.Errorf("Medusa differs from upstream reference:\n got %s\nwant %s", got, v.Medusa)
			}
		})
	}
}

type signingTransport func(*http.Request) (*http.Response, error)

func (f signingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func isolatePlaybackSession(t *testing.T) {
	t.Helper()
	old := playbackDevice
	playbackDevice = nil
	t.Cleanup(func() { playbackDevice = old })
}

// Exercise the real resolver, including empty-response device renewal. A signer
// that exists but is omitted from either request must fail this regression test.
func TestResolvePlaySignsRequestsAndRenewal(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}
	playCalls, registerCalls := 0, 0
	var payloads [][]byte
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			registerCalls++
			return jsonResponse(fmt.Sprintf(`{"device_id_str":"12345678901234567%02d","install_id_str":"2345678901234567890"}`, registerCalls)), nil
		}
		if req.URL.Path == "/reading/bookapi/video_tab/multi_video_model/v1/" {
			if registerCalls != 1 {
				t.Fatal("video_tab ran after a successful renewed POST")
			}
			return jsonResponse(""), nil
		}
		if !strings.HasPrefix(req.URL.String(), mobileVideoAPI+"?") {
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
		}
		playCalls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, body)
		if req.URL.Query().Get("device_id") != fmt.Sprintf("12345678901234567%02d", playCalls) {
			t.Error("empty response did not renew the identity")
		}
		ts, _ := strconv.ParseUint(req.Header.Get("x-khronos"), 10, 32)
		medusa, err := base64.StdEncoding.DecodeString(req.Header.Get("x-medusa"))
		if err != nil || len(medusa) < 24 {
			t.Fatal("missing v5 envelope")
		}
		prefix := append([]byte(nil), medusa[:20]...)
		for i := range prefix {
			prefix[i] ^= le32(uint32(ts))[i&3]
		}
		if !bytes.Equal(prefix, join(le32(5), mobileMagic)) {
			t.Error("renewal sent an obsolete signature")
		}
		if req.Header.Get("Cookie") != "" {
			t.Error("model request leaked registration cookies")
		}
		if playCalls == 1 {
			return jsonResponse(""), nil
		}
		return jsonResponse(`{"code":0,"data":{"123":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":720,"height":1280}]}}}}`), nil
	})}
	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if playCalls != 2 || registerCalls != 2 {
		t.Fatalf("play=%d register=%d", playCalls, registerCalls)
	}
	if !bytes.Equal(payloads[0], payloads[1]) {
		t.Error("renewal changed compressed payload")
	}
	if info.VID != "123" || info.Quality != "720P" {
		t.Fatalf("unexpected source %+v", info)
	}
}

// ResolvePlay plays the highest resolution up to the one asked for, the
// lowest when all are higher, and tells them all.
func TestResolvePlayQuality(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		row := func(px, w, h int) string {
			return `{"main_url":"https://example.fqnovelvod.com/` + strconv.Itoa(px) + `.mp4","codec_type":"h264","width":` + strconv.Itoa(w) + `,"height":` + strconv.Itoa(h) + `}`
		}
		body := `{"code":0,"data":{"123":{"video_model":{"video_list":[` + row(540, 540, 960) + `,` + row(1080, 1080, 1920) + `,` + row(720, 720, 1280) + `]}}}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(body))}, nil
	})}
	for _, c := range []struct {
		max  int
		want string
	}{{0, "1080P"}, {1080, "1080P"}, {900, "720P"}, {720, "720P"}, {540, "540P"}, {360, "540P"}} {
		info, err := ResolvePlay(context.Background(), "123", c.max)
		if err != nil {
			t.Fatal(err)
		}
		if info.Quality != c.want || !strings.HasSuffix(info.URL, "/"+strings.TrimSuffix(c.want, "P")+".mp4") {
			t.Errorf("max %d: got %s %s, want %s", c.max, info.Quality, info.URL, c.want)
		}
		if fmt.Sprint(info.Qualities) != "[1080 720 540]" {
			t.Errorf("qualities %v", info.Qualities)
		}
	}
}

// A valid primary can still return 403 at download time. Keep the same
// rendition's alternate CDNs instead of losing them during resolution.
func TestResolvePlayPreservesRenditionBackups(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient := httpClient
	t.Cleanup(func() { httpClient = oldClient })
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		return jsonResponse(`{"code":0,"data":{"video_model_data":{"123":{"video_model":{"video_list":[
			{"main_url":"https://primary.fqnovelvod.com/1080.mp4","backup_url":"https://backup.fqnovelvod.com/1080.mp4","backup_url_list":["https://primary.fqnovelvod.com/1080.mp4","https://untrusted.example/1080.mp4","https://third.fqnovelvod.com/1080.mp4","https://fourth.fqnovelvod.com/1080.mp4"],"codec_type":"bytevc1","width":1920,"height":1080},
			{"main_url":"https://primary.fqnovelvod.com/720.mp4","backup_url":"https://backup.fqnovelvod.com/720.mp4","codec_type":"h264","width":1280,"height":720}
		]}}}}}`), nil
	})}
	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://primary.fqnovelvod.com/1080.mp4" || info.Quality != "1080P" || info.Codec != "hevc" {
		t.Fatalf("unexpected primary: %+v", info)
	}
	if got := strings.Join(info.BackupURLs, ","); got != "https://backup.fqnovelvod.com/1080.mp4,https://third.fqnovelvod.com/1080.mp4" {
		t.Fatalf("backups crossed renditions, retained duplicates/untrusted hosts, or exceeded the bound: %s", got)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// Responses must resolve the exact episode across both current response maps
// and string/object video_model encodings. Bad models must not trigger a loop.
func TestResolvePlayModelShapes(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	model := `{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"bytevc1","width":1080,"height":1920}]}`
	encoded, _ := json.Marshal(model)
	for _, c := range []struct {
		name, body string
		wantErr    bool
		calls      int
	}{
		{"flat object", `{"code":0,"data":{"123":{"video_model":` + model + `}}}`, false, 1},
		{"flat string", `{"code":0,"data":{"123":{"video_model":` + string(encoded) + `}}}`, false, 1},
		{"1004 object", `{"code":0,"data":{"1004":{"123":{"video_model":` + model + `}}}}`, false, 1},
		{"1004 string", `{"code":0,"data":{"1004":{"123":{"video_model":` + string(encoded) + `}}}}`, false, 1},
		{"video_model_data", `{"code":0,"data":{"video_model_data":{"123":{"video_model":` + model + `}}}}`, false, 1},
		{"empty", ``, true, 4}, {"business error", `{"code":100103}`, true, 1},
		{"invalid JSON", `invalid`, true, 1}, {"invalid 1004", `{"data":{"1004":[]}}`, true, 1},
		{"other episode", `{"code":0,"data":{"1004":{"456":{"video_model":` + model + `}}}}`, true, 1},
		{"invalid model", `{"data":{"123":{"video_model":"invalid"}}}`, true, 1},
		{"unsupported codec", `{"data":{"123":{"video_model":{"video_list":[{"codec_type":"bytevc2","width":1920,"height":1080}]}}}}`, true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
			calls, registers := 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
				}
				if req.URL.Path != "/novel/player/multi_video_model/player/v1" && req.URL.Path != "/reading/bookapi/video_tab/multi_video_model/v1/" {
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
				}
				calls++
				return jsonResponse(c.body), nil
			})}
			info, err := ResolvePlay(context.Background(), "123", 0)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, c.wantErr)
			}
			if !c.wantErr && (info.VID != "123" || info.Codec != "hevc" || info.Quality != "1080P") {
				t.Fatalf("unexpected source %+v", info)
			}
			wantRegisters := 0
			if c.name == "empty" {
				wantRegisters = 1
			}
			if calls != c.calls || registers != wantRegisters {
				t.Fatalf("calls=%d registers=%d", calls, registers)
			}
		})
	}
}

// A reused playback identity may stop working. Renew it once on empty, while
// keeping the independent detail SDK profile on a separate identity.
func TestSignedJSONRenewsSingleUsePlaybackDevices(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			var mu sync.Mutex
			registers, calls := 0, 0
			seen := map[string]bool{}
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(fmt.Sprintf(`{"device_id_str":"34567890123456789%02d","install_id_str":"4567890123456789012"}`, registers)), nil
				}
				calls++
				id := req.URL.Query().Get("device_id")
				if seen[id] {
					return jsonResponse(""), nil
				}
				seen[id] = true
				return jsonResponse(`{"ok":true}`), nil
			})}
			var wg sync.WaitGroup
			for _, api := range []string{mobileVideoAPI, mobileVideoAPI, detailAPI, mobileVideoAPI} {
				call := func() {
					defer wg.Done()
					if _, err := signedJSON(context.Background(), api, mobileVideoPayload("123")); err != nil {
						t.Error(err)
					}
				}
				wg.Add(1)
				if concurrent {
					go call()
				} else {
					call()
				}
			}
			wg.Wait()
			if calls != 8 || registers != 4 || len(seen) != 4 {
				t.Fatalf("calls=%d registers=%d devices=%d, want 8, 4, 4", calls, registers, len(seen))
			}
		})
	}
}

func TestSignedJSONRetryBoundaries(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, c := range []struct {
		name         string
		status       int
		body         string
		transportErr error
		cancel       bool
		wantCalls    int
		wantErr      error
	}{
		{name: "empty", status: 200, wantCalls: 4, wantErr: errEmpty},
		{name: "cancel during retry", status: 200, cancel: true, wantCalls: 1, wantErr: context.Canceled},
		{name: "rate limited", status: 429, wantCalls: 1},
		{name: "server error", status: 503, wantCalls: 1},
		{name: "business response", status: 200, body: `{"code":1001}`, wantCalls: 1},
		{name: "network error", transportErr: io.ErrUnexpectedEOF, wantCalls: 1, wantErr: io.ErrUnexpectedEOF},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			registers, calls := 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(fmt.Sprintf(`{"device_id_str":"34567890123456789%02d","install_id_str":"4567890123456789012"}`, registers)), nil
				}
				calls++
				if c.cancel {
					cancel()
				}
				if c.transportErr != nil {
					return nil, c.transportErr
				}
				res := jsonResponse(c.body)
				res.StatusCode = c.status
				return res, nil
			})}
			_, err := signedJSON(ctx, mobileVideoAPI, mobileVideoPayload("123"))
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("error=%v, want %v", err, c.wantErr)
			}
			if c.status >= 400 && err == nil {
				t.Error("HTTP error was ignored")
			}
			if c.body != "" && err != nil {
				t.Error(err)
			}
			wantRegisters := 1
			if c.name == "empty" {
				wantRegisters = 2
			}
			if calls != c.wantCalls || registers != wantRegisters {
				t.Fatalf("calls=%d registers=%d, want %d, %d", calls, registers, c.wantCalls, wantRegisters)
			}
			if c.name == "empty" && playbackDevice != nil {
				t.Fatal("rejected playback identity remained cached after the final empty response")
			}
		})
	}
}

func TestSignedJSONRegistrationFailure(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}
	calls := 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/service/2/device_register/" {
			t.Errorf("model request after failed registration")
		}
		res := jsonResponse("")
		res.StatusCode = http.StatusTooManyRequests
		return res, nil
	})}
	for i := 0; i < 2; i++ {
		if _, err := signedJSON(context.Background(), mobileVideoAPI, mobileVideoPayload("123")); err == nil {
			t.Fatal("expected registration failure")
		}
	}
	if calls != 1 {
		t.Fatalf("registration calls=%d, want 1 during cooldown", calls)
	}

	deviceRetry = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := signedJSON(ctx, mobileVideoAPI, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 || !deviceRetry.IsZero() {
		t.Fatal("cancelled caller sent a request or imposed a cooldown")
	}
}

func TestDeviceRegistrationCooldownPreservesCause(t *testing.T) {
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	for _, tc := range []struct {
		name    string
		status  int
		cause   error
		message string
	}{
		{name: "HTTP rejection", status: http.StatusTooManyRequests, message: "HTTP 429"},
		{name: "connection failure", cause: io.ErrUnexpectedEOF, message: "unexpected EOF"},
		{name: "timeout", cause: context.DeadlineExceeded, message: "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deviceRetry = time.Time{}
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if tc.cause != nil {
					return nil, tc.cause
				}
				res := jsonResponse("")
				res.StatusCode = tc.status
				return res, nil
			})}
			for i := 0; i < 2; i++ {
				_, err := newDevice(context.Background())
				if err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("attempt %d lost registration failure: %v", i, err)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Fatalf("attempt %d lost error chain: %v", i, err)
				}
			}
			if calls != 1 {
				t.Fatalf("registration calls=%d, want 1 during cooldown", calls)
			}
			deviceRetry = time.Now().Add(-time.Second)
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
			})}
			if _, err := newDevice(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !deviceRetry.IsZero() || deviceRetryErr != nil {
				t.Fatal("successful registration retained a stale failure")
			}
		})
	}
}

func TestPlaybackAvoidsRedundantRegistrations(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}
	registers, models, tabs := 0, 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/service/2/device_register/":
			registers++
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		case "/reading/bookapi/video_tab/multi_video_model/v1/":
			tabs++
			return jsonResponse(""), nil
		case "/novel/player/multi_video_model/player/v1":
			models++
			var payload struct {
				Mixed map[string][]string `json:"mixed_video_id_map"`
			}
			gz, err := gzip.NewReader(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			if err := json.NewDecoder(gz).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			vid := payload.Mixed["1004"][0]
			return jsonResponse(fmt.Sprintf(`{"code":0,"data":{"%s":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":1920,"height":1080}]}}}}`, vid)), nil
		default:
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
			return nil, nil
		}
	})}
	// Playing, prefetching, and changing quality all resolve models. Successful
	// v5 requests can share one anonymous playback identity.
	for _, vid := range []string{"123", "456", "123"} {
		if _, err := ResolvePlay(context.Background(), vid, 0); err != nil {
			t.Fatal(err)
		}
	}
	if registers != 1 || models != 3 || tabs != 0 {
		t.Fatalf("registers=%d models=%d tabs=%d; want 1, 3, 0 with identity reuse", registers, models, tabs)
	}
	// Concurrent cold callers must share one registration as well.
	playbackDevice = nil
	registers, models, tabs = 0, 0, 0
	var wg sync.WaitGroup
	for _, vid := range []string{"123", "456", "123"} {
		wg.Go(func() {
			if _, err := ResolvePlay(context.Background(), vid, 0); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if registers != 1 || models != 3 || tabs != 0 {
		t.Fatalf("concurrent registers=%d models=%d tabs=%d; want 1, 3, 0", registers, models, tabs)
	}
}

func TestPlaybackConcurrentRequestsShareIdentity(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}
	var mu sync.Mutex
	registers, models := 0, 0
	playbackID := ""
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/service/2/device_register/" {
			registers++
			return jsonResponse(fmt.Sprintf(`{"device_id_str":"12345678901234567%02d","install_id_str":"2345678901234567890"}`, registers)), nil
		}
		id := req.URL.Query().Get("device_id")
		if req.URL.Path == "/novel/player/multi_video_model/player/v1" {
			models++
			if playbackID != "" && playbackID != id {
				t.Error("concurrent playback replaced a working identity")
			}
			playbackID = id
		} else if id == playbackID {
			t.Error("detail SDK profile shared playback's identity")
		}
		return jsonResponse(`{"ok":true}`), nil
	})}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := signedJSON(context.Background(), mobileVideoAPI, mobileVideoPayload("123")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for range 2 {
		if _, err := signedJSON(context.Background(), detailAPI, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if registers != 3 || models != 4 {
		t.Fatalf("registers=%d models=%d; want 1 playback + 2 detail registrations and 4 models", registers, models)
	}
}

func TestPlaybackQueueCanBeCanceled(t *testing.T) {
	isolatePlaybackSession(t)
	playbackGate <- struct{}{}
	defer func() { <-playbackGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := signedJSON(ctx, mobileVideoAPI, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued playback ignored cancellation: %v", err)
	}
	if playbackDevice != nil {
		t.Fatal("canceled queued playback registered an identity")
	}
}

func TestPlaybackHTTPRejectionsDoNotRegisterAgain(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, status := range []int{403, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			playbackDevice = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890"}
			registers, models := 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
				}
				if req.URL.Path != "/novel/player/multi_video_model/player/v1" {
					t.Errorf("HTTP rejection triggered another endpoint: %s", req.URL.Path)
				}
				models++
				res := jsonResponse("")
				res.StatusCode = status
				return res, nil
			})}
			for range 2 {
				if _, err := ResolvePlay(context.Background(), "123", 0); err == nil || !strings.Contains(err.Error(), strconv.Itoa(status)) {
					t.Fatalf("HTTP rejection was lost: %v", err)
				}
			}
			if registers != 0 || models != 2 {
				t.Fatalf("registers=%d models=%d; want 0, 2 without automatic retries", registers, models)
			}
		})
	}
}

func TestDeviceRegistrationRespectsRetryAfter(t *testing.T) {
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	for _, header := range []string{"120", time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			deviceRetry = time.Time{}
			calls := 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				res := jsonResponse("")
				res.StatusCode = http.StatusTooManyRequests
				res.Header.Set("Retry-After", header)
				return res, nil
			})}
			for range 2 {
				if _, err := newDevice(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
					t.Fatalf("rate limit was lost: %v", err)
				}
			}
			if calls != 1 || time.Until(deviceRetry) < 115*time.Second {
				t.Fatalf("calls=%d cooldown=%v; upstream asked for 2 minutes", calls, time.Until(deviceRetry))
			}
		})
	}
}

func TestColdPlaybackHTTPBoundaries(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, status := range []int{403, 429, 404, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			registers, tabs, models := 0, 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/service/2/device_register/":
					registers++
					return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
				case "/reading/bookapi/video_tab/multi_video_model/v1/":
					tabs++
				default:
					models++
				}
				res := jsonResponse("")
				res.StatusCode = status
				return res, nil
			})}
			if _, err := ResolvePlay(context.Background(), "123", 0); err == nil || !strings.Contains(err.Error(), strconv.Itoa(status)) {
				t.Fatalf("HTTP rejection was lost: %v", err)
			}
			if registers != 1 || tabs != 0 || models != 1 {
				t.Fatalf("registers=%d tabs=%d models=%d; HTTP failures must not rotate identity or probe legacy routes", registers, tabs, models)
			}
		})
	}
}

// Registration still succeeds when deprecated model routes return empty 200s.
// The production resolver must use the current gzip/v5 route and reuse its
// identity across episodes, without probing the rejected legacy routes first.
func TestResolvePlayCurrentMobileProtocol(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	deviceRetry = time.Time{}
	registers, models, legacy := 0, 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			registers++
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		if req.URL.Path != "/novel/player/multi_video_model/player/v1" {
			legacy++
			return jsonResponse(""), nil
		}
		models++
		for _, name := range []string{"x-khronos", "x-medusa", "x-helios", "x-ss-stub"} {
			if req.Header.Get(name) == "" {
				t.Errorf("missing %s", name)
			}
		}
		for _, name := range []string{"Cookie", "x-argus", "x-ladon", "x-gorgon"} {
			if req.Header.Get(name) != "" {
				t.Errorf("v5 request carries %s", name)
			}
		}
		if req.Header.Get("Content-Encoding") != "gzip" {
			t.Fatal("model body is not gzip")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		digest := md5.Sum(body)
		if req.Header.Get("x-ss-stub") != strings.ToUpper(hex.EncodeToString(digest[:])) {
			t.Error("digest does not cover gzip bytes")
		}
		ts, _ := strconv.ParseUint(req.Header.Get("x-khronos"), 10, 32)
		qh := sm3([]byte(req.URL.RawQuery))
		if signingIV(signingIV(signingIV(0x20230928, qh), digest[:]), le32(uint32(ts)))&15 != 12 {
			t.Error("unsupported v5 branch")
		}
		if req.URL.Query().Has("ts") {
			t.Error("v5 request contains legacy ts parameter")
		}
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		var payload struct {
			Mixed map[string][]string `json:"mixed_video_id_map"`
			Scene string              `json:"dr_scene"`
			Biz   struct {
				Level       int  `json:"device_level"`
				Definitions bool `json:"need_all_video_definition"`
			} `json:"biz_param"`
		}
		if err := json.NewDecoder(gz).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Scene != "player" || payload.Biz.Level != 1 || !payload.Biz.Definitions {
			t.Fatal("wrong mobile playback profile")
		}
		vid := payload.Mixed["1004"][0]
		return jsonResponse(fmt.Sprintf(`{"code":0,"data":{"1004":{"%s":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":1920,"height":1080}]}}}}}`, vid)), nil
	})}
	for _, vid := range []string{"123", "456"} {
		info, err := ResolvePlay(context.Background(), vid, 0)
		if err != nil {
			t.Fatal(err)
		}
		if info.VID != vid || info.Quality != "1080P" {
			t.Fatalf("wrong episode/quality: %+v", info)
		}
	}
	if registers != 1 || models != 2 || legacy != 0 {
		t.Fatalf("registers=%d models=%d legacy=%d", registers, models, legacy)
	}
}

// Synthetic vectors from N3urda/hongguoTV's GPL v5 fixtures (18c37c2).
// Digest vectors cover all 32 rotations; carrier vectors cover all four tables.
func TestPlaybackV5ReferenceVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/playback-v5.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Carrier []struct {
			TS                         uint32
			Source, Key, IV, Encrypted string
		}
		F13 []struct {
			TS                  uint32
			Query, Body, Digest string
		}
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Carrier) != 4 || len(vectors.F13) != 32 {
		t.Fatal("incomplete v5 reference vectors")
	}
	for i, v := range vectors.Carrier {
		got := newSigningCarrier(unhex(v.Key), v.TS, true).encrypt(unhex(v.Source), unhex(v.IV))
		if hex.EncodeToString(got) != v.Encrypted {
			t.Errorf("carrier variant %d differs from reference", i)
		}
	}
	for i, v := range vectors.F13 {
		got := hashF13Profile(unhex(v.Query), unhex(v.Body), v.TS, true)
		if hex.EncodeToString(got[:16]) != v.Digest {
			t.Errorf("digest rotation %d differs from reference", i)
		}
		if got[16]&0x24 != 0x24 {
			t.Errorf("digest rotation %d lost v5 checksum flags", i)
		}
	}
}

func TestPlaybackV5BindsIdentityAndWireBytes(t *testing.T) {
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic", Cookie: "ttreq=anonymous"}
	now := time.Unix(1800000000, 0)
	for n := range 32 {
		req, err := signedMobileRequest(context.Background(), mobileVideoPayload("123"), dev, now.Add(time.Duration(n)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := req.URL.RawQuery
		ts := uint32(now.Unix()) + uint32(n)
		sign := func(q string, b []byte, stamp uint32, d *device, random uint32) string {
			return buildMobileMedusa(q, b, stamp, d, "synthetic", now.UnixMilli(), func() uint32 { return random })
		}
		want := sign(query, body, ts, dev, 0x12345678)
		other := *dev
		other.DeviceID = "3456789012345678901"
		for _, got := range []string{
			sign(query+"&changed=1", body, ts, dev, 0x12345678),
			sign(query, append(append([]byte(nil), body...), 0), ts, dev, 0x12345678),
			sign(query, body, ts+1, dev, 0x12345678),
			sign(query, body, ts, &other, 0x12345678),
			sign(query, body, ts, dev, 0x87654321),
		} {
			if got == want {
				t.Fatalf("rotation %d: v5 signature did not bind an input", n)
			}
		}
		q := req.URL.Query()
		if q.Get("device_id") != dev.DeviceID || q.Get("iid") != dev.IID || q.Get("cdid") != dev.CDID {
			t.Fatal("signing changed registered identity")
		}
		if q.Get("version_code") != "74332" || q.Get("channel") != "vivo_8662_64" || q.Has("ts") {
			t.Fatal("wrong v5 profile")
		}
		if req.Header.Get("Cookie") != "" {
			t.Fatal("v5 model carries cookies")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := signedMobileRequest(ctx, mobileVideoPayload("123"), dev, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("signing ignored cancellation: %v", err)
	}
}

// The v5 POST can return empty 200s for a valid identity. Recover through the
// same episode's v5 video_tab GET before registering another identity.
func TestResolvePlayV5VideoTabRecovery(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	deviceRetry = time.Time{}
	registers, posts, gets := 0, 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/service/2/device_register/":
			registers++
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		case "/novel/player/multi_video_model/player/v1":
			posts++
			return jsonResponse(""), nil
		case "/reading/bookapi/video_tab/multi_video_model/v1/":
			gets++
			if req.Method != http.MethodGet || req.ContentLength != 0 {
				t.Fatal("video_tab must be a bodyless GET")
			}
			if req.Body != nil && req.Body != http.NoBody {
				t.Fatal("GET contains a body")
			}
			for _, name := range []string{"Content-Type", "Content-Encoding", "x-ss-stub", "x-ss-req-ticket", "x-argus", "x-ladon", "x-gorgon", "Cookie"} {
				if req.Header.Get(name) != "" {
					t.Errorf("GET carries obsolete header %s", name)
				}
			}
			for _, name := range []string{"x-khronos", "x-helios", "x-medusa"} {
				if req.Header.Get(name) == "" {
					t.Errorf("GET missing v5 %s", name)
				}
			}
			q := req.URL.Query()
			for name, want := range map[string]string{"device_id": "1234567890123456789", "iid": "2345678901234567890", "video_id_type": "0", "device_level": "1", "need_all_video_definition": "1", "need_mp4_align": "0", "use_os_player": "0", "video_platform": "1024", "source": "0", "from_video_id": "", "version_code": "74332", "version_name": "7.4.3.32"} {
				if !q.Has(name) || q.Get(name) != want {
					t.Errorf("%s=%q want %q", name, q.Get(name), want)
				}
			}
			ts, _ := strconv.ParseUint(req.Header.Get("x-khronos"), 10, 32)
			bh := md5.Sum(nil)
			if signingIV(signingIV(signingIV(0x20230928, sm3([]byte(req.URL.RawQuery))), bh[:]), le32(uint32(ts)))&15 != 12 {
				t.Error("GET digest must sign MD5(empty) with v5 branch 12")
			}
			vid := q.Get("video_id")
			if vid != "123" && vid != "456" {
				t.Fatalf("wrong requested episode %q", vid)
			}
			return jsonResponse(fmt.Sprintf(`{"code":0,"data":{"video_model_data":{"%s":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":1920,"height":1080}]}}}}}`, vid)), nil
		default:
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
			return nil, nil
		}
	})}
	for _, vid := range []string{"123", "456"} {
		info, err := ResolvePlay(context.Background(), vid, 0)
		if err != nil {
			t.Fatal(err)
		}
		if info.VID != vid || info.Quality != "1080P" {
			t.Fatalf("wrong media %+v", info)
		}
	}
	var wg sync.WaitGroup
	for _, vid := range []string{"123", "456"} {
		wg.Go(func() {
			info, err := ResolvePlay(context.Background(), vid, 0)
			if err != nil {
				t.Error(err)
			} else if info.VID != vid {
				t.Errorf("wrong VID %s", info.VID)
			}
		})
	}
	wg.Wait()
	if registers != 1 || posts != 4 || gets != 4 {
		t.Fatalf("registers=%d posts=%d gets=%d; fallback must reuse the identity", registers, posts, gets)
	}
}

func TestV5VideoTabRecoveryBoundaries(t *testing.T) {
	oldClient, oldRetry, oldRetryErr := httpClient, deviceRetry, deviceRetryErr
	t.Cleanup(func() { httpClient, deviceRetry, deviceRetryErr = oldClient, oldRetry, oldRetryErr })
	for _, tc := range []struct {
		name, body               string
		status                   int
		err                      error
		cancel                   bool
		wantCalls, wantRegisters int
	}{
		{name: "both routes empty", status: 200, wantCalls: 4, wantRegisters: 2},
		{name: "forbidden", status: 403, wantCalls: 2, wantRegisters: 1},
		{name: "rate limited", status: 429, wantCalls: 2, wantRegisters: 1},
		{name: "server error", status: 503, wantCalls: 2, wantRegisters: 1},
		{name: "business rejection", status: 200, body: `{"code":110001}`, wantCalls: 2, wantRegisters: 1},
		{name: "invalid JSON", status: 200, body: `invalid`, wantCalls: 2, wantRegisters: 1},
		{name: "wrong episode", status: 200, body: `{"data":{"video_model_data":{"456":{"video_model":{"video_list":[]}}}}}`, wantCalls: 2, wantRegisters: 1},
		{name: "transport error", err: io.ErrUnexpectedEOF, wantCalls: 2, wantRegisters: 1},
		{name: "cancel during GET", status: 200, cancel: true, wantCalls: 2, wantRegisters: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatePlaybackSession(t)
			deviceRetry = time.Time{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, registers := 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/service/2/device_register/" {
					registers++
					return jsonResponse(fmt.Sprintf(`{"device_id_str":"12345678901234567%02d","install_id_str":"2345678901234567890"}`, registers)), nil
				}
				calls++
				if req.URL.Path == "/novel/player/multi_video_model/player/v1" {
					return jsonResponse(""), nil
				}
				if req.URL.Path != "/reading/bookapi/video_tab/multi_video_model/v1/" {
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
				}
				if req.URL.Query().Get("video_id") != "123" {
					t.Fatal("fallback changed episode")
				}
				if req.URL.Query().Get("device_id") != fmt.Sprintf("12345678901234567%02d", registers) {
					t.Fatal("fallback changed identity")
				}
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
			_, err := ResolvePlay(ctx, "123", 0)
			if err == nil {
				t.Fatal("failed fallback was accepted")
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("original error lost: %v", err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if tc.status >= 400 && !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Fatalf("HTTP rejection lost: %v", err)
			}
			if calls != tc.wantCalls || registers != tc.wantRegisters {
				t.Fatalf("calls=%d registers=%d want %d,%d", calls, registers, tc.wantCalls, tc.wantRegisters)
			}
			if tc.name == "both routes empty" && playbackDevice != nil {
				t.Fatal("exhausted identity remained cached")
			}
		})
	}
}

func TestV5VideoTabSigningEmptyBody(t *testing.T) {
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic"}
	for n := range 32 {
		now := time.Unix(1800000000+int64(n), 0)
		req, err := signedMobileTabRequest(context.Background(), "123", dev, now)
		if err != nil {
			t.Fatal(err)
		}
		bh := md5.Sum(nil)
		qh := sm3([]byte(req.URL.RawQuery))
		if signingIV(signingIV(signingIV(0x20230928, qh), bh[:]), le32(uint32(now.Unix())))&15 != 12 {
			t.Fatalf("rotation %d signs wrong empty body", n)
		}
		constant := func() uint32 { return 0x12345678 }
		empty := buildMobileMedusa(req.URL.RawQuery, []byte{}, uint32(now.Unix()), dev, "synthetic", now.UnixMilli(), constant)
		oldNil := buildMobileMedusa(req.URL.RawQuery, nil, uint32(now.Unix()), dev, "synthetic", now.UnixMilli(), constant)
		if empty == oldNil {
			t.Fatalf("rotation %d reused zeroed legacy body digest", n)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := signedMobileTabRequest(ctx, "123", dev, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("GET signing ignored cancellation: %v", err)
	}
}
