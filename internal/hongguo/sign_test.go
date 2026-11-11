package hongguo

import (
	"bytes"
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

// Exercise the real resolver, including empty-response device renewal. A signer
// that exists but is omitted from either request must fail this regression test.
func TestResolvePlaySignsRequestsAndRenewal(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}
	playCalls, registerCalls := 0, 0
	var payloads [][]byte
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		respond := func(s string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(s))}, nil
		}
		if req.URL.Path == "/service/2/device_register/" {
			registerCalls++
			if registerCalls <= 2 {
				return respond(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`)
			}
			return respond(`{"device_id_str":"3456789012345678901","install_id_str":"4567890123456789012"}`)
		}
		if req.URL.Path == "/reading/bookapi/video_tab/multi_video_model/v1/" {
			return respond(`{"code":100103}`)
		}
		if req.URL.Path != "/novel/player/multi_video_model/v1/" {
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
		}
		playCalls++
		b, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, b)
		for _, h := range []string{"x-ss-req-ticket", "sdk-version", "x-khronos", "x-gorgon", "x-ladon", "x-argus", "x-helios", "x-tt-store-region", "x-tt-store-region-src"} {
			if req.Header.Get(h) == "" {
				t.Errorf("play request %d missing %s", playCalls, h)
			}
		}
		if req.Header.Get("x-medusa") != "" {
			t.Error("playback renewal reintroduced Medusa")
		}
		q := req.URL.Query()
		if q.Get("_rticket") != req.Header.Get("x-ss-req-ticket") || q.Get("ts") != req.Header.Get("x-khronos") {
			t.Error("signed timestamps do not match URL")
		}
		timestamp, err := strconv.ParseUint(q.Get("ts"), 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		bh := md5.Sum(b)
		if branch := signingBranch(sm3([]byte(req.URL.RawQuery)), bh[:], uint32(timestamp)); branch != 0 {
			t.Errorf("unsupported branch %d", branch)
		}
		gorgon, err := hex.DecodeString(req.Header.Get("x-gorgon"))
		if err != nil || len(gorgon) != 26 {
			t.Fatal("invalid Gorgon header")
		}
		random := uint16(gorgon[2]) | uint16(gorgon[3])<<8
		if req.Header.Get("x-gorgon") != xGorgon(req.URL.RawQuery, b, uint32(timestamp), random) {
			t.Error("signature does not cover transmitted bytes")
		}
		wantID := "1234567890123456789"
		if playCalls == 2 {
			wantID = "3456789012345678901"
		}
		if q.Get("device_id") != wantID {
			t.Errorf("request %d used wrong device", playCalls)
		}
		wantHost := "api5-normal-sinfonlineb.fqnovel.com"
		if playCalls == 2 {
			wantHost = "api5-normal-hl.fqnovel.com"
		}
		if req.URL.Hostname() != wantHost {
			t.Errorf("request %d host=%s, want %s", playCalls, req.URL.Hostname(), wantHost)
		}
		if playCalls == 1 {
			return respond("")
		}
		return respond(`{"code":0,"data":{"123":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":720,"height":1280}]}}}}`)
	})}
	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if playCalls != 2 || registerCalls != 3 {
		t.Fatalf("play=%d register=%d", playCalls, registerCalls)
	}
	if !bytes.Equal(payloads[0], payloads[1]) {
		t.Error("renewal changed the playback body")
	}
	if info.URL != "https://example.fqnovelvod.com/video.mp4" || info.Quality != "720P" {
		t.Fatalf("unexpected source %+v", info)
	}
}

// ResolvePlay plays the highest resolution up to the one asked for, the
// lowest when all are higher, and tells them all.
func TestResolvePlayQuality(t *testing.T) {
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

// The legacy endpoint can stay empty while video_tab returns a complete model.
// A resolver must use the new path first and keep the exact requested episode.
func TestResolvePlayVideoTab(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	model := `{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"bytevc1","width":1080,"height":1920}]}`
	encodedModel, _ := json.Marshal(model)
	for _, c := range []struct {
		name, body string
		fallback   bool
	}{
		{"object", `{"code":0,"data":{"video_model_data":{"123":{"video_model":` + model + `}}}}`, false},
		{"string", `{"code":0,"data":{"video_model_data":{"123":{"video_model":` + string(encodedModel) + `}}}}`, false},
		{"empty", ``, true},
		{"business error", `{"code":100103}`, true},
		{"invalid JSON", `invalid`, true},
		{"other episode", `{"code":0,"data":{"video_model_data":{"456":{"video_model":` + model + `}}}}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			deviceRetry = time.Time{}
			registers, gets, posts := 0, 0, 0
			httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/service/2/device_register/":
					registers++
					res := jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`)
					res.Header.Add("Set-Cookie", "ttreq=anonymous-token; Path=/; HttpOnly")
					res.Header.Add("Set-Cookie", "store-region=cn; Path=/")
					res.Header.Add("Set-Cookie", "sessionid=unrelated-token; Path=/")
					return res, nil
				case "/reading/bookapi/video_tab/multi_video_model/v1/":
					gets++
					if req.Method != http.MethodGet || req.ContentLength != 0 {
						t.Errorf("video_tab request method=%s length=%d", req.Method, req.ContentLength)
					}
					q := req.URL.Query()
					for k, want := range map[string]string{
						"video_id": "123", "video_id_type": "0", "device_level": "1",
						"need_all_video_definition": "1", "need_mp4_align": "0", "use_os_player": "0",
						"video_platform": "1024", "source": "0", "from_video_id": "",
						"version_code": "73932", "version_name": "7.3.9.32",
					} {
						if !q.Has(k) || q.Get(k) != want {
							t.Errorf("%s=%q, want %q", k, q.Get(k), want)
						}
					}
					ts, _ := strconv.ParseUint(req.Header.Get("x-khronos"), 10, 32)
					if signingBranch(sm3([]byte(req.URL.RawQuery)), make([]byte, 16), uint32(ts)) != 0 {
						t.Error("GET body hash must be zero bytes")
					}
					if req.Header.Get("x-medusa") == "" || req.Header.Get("x-gorgon") == "" {
						t.Error("GET is unsigned")
					}
					if req.Header.Get("x-ss-stub") != "" {
						t.Error("GET carries a POST body digest")
					}
					if !strings.Contains(req.Header.Get("Cookie"), "ttreq=anonymous-token") || strings.Contains(req.Header.Get("Cookie"), "sessionid") {
						t.Error("request did not keep only anonymous registration cookies")
					}
					return jsonResponse(c.body), nil
				case "/novel/player/multi_video_model/v1/":
					posts++
					if !c.fallback {
						t.Error("successful GET was followed by a legacy POST")
					}
					return jsonResponse(`{"code":0,"data":{"123":{"video_model":` + model + `}}}`), nil
				default:
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
					return nil, nil
				}
			})}
			info, err := ResolvePlay(context.Background(), "123", 0)
			if err != nil {
				t.Fatal(err)
			}
			if info.VID != "123" || info.Codec != "hevc" || info.Quality != "1080P" {
				t.Fatalf("unexpected source %+v", info)
			}
			wantPosts := 0
			if c.fallback {
				wantPosts = 1
			}
			if gets != 1 || posts != wantPosts || registers != 1+wantPosts {
				t.Fatalf("gets=%d posts=%d registers=%d", gets, posts, registers)
			}
		})
	}
}

// A successful device may become single-use. Neither the next episode nor a
// concurrent season lookup should first spend a request on that identity.
func TestSignedJSONFreshDevices(t *testing.T) {
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
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
					t.Errorf("identity %s reused for %s", id, req.URL.Path)
					return jsonResponse(""), nil
				}
				seen[id] = true
				return jsonResponse(`{"ok":true}`), nil
			})}
			var wg sync.WaitGroup
			for _, api := range []string{videoAPI, videoAPI, detailAPI, videoAPI} {
				call := func() {
					defer wg.Done()
					if _, err := signedJSON(context.Background(), api, []byte(`{}`)); err != nil {
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
			if calls != 4 || registers != 4 || len(seen) != 4 {
				t.Fatalf("calls=%d registers=%d devices=%d, want 4 each", calls, registers, len(seen))
			}
		})
	}
}

func TestSignedJSONRetryBoundaries(t *testing.T) {
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
		{name: "empty", status: 200, wantCalls: 2, wantErr: errEmpty},
		{name: "cancel during retry", status: 200, cancel: true, wantCalls: 1, wantErr: context.Canceled},
		{name: "rate limited", status: 429, wantCalls: 1},
		{name: "server error", status: 503, wantCalls: 1},
		{name: "business response", status: 200, body: `{"code":1001}`, wantCalls: 1},
		{name: "network error", transportErr: io.ErrUnexpectedEOF, wantCalls: 1, wantErr: io.ErrUnexpectedEOF},
	} {
		t.Run(c.name, func(t *testing.T) {
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
			_, err := signedJSON(ctx, videoAPI, []byte(`{}`))
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("error=%v, want %v", err, c.wantErr)
			}
			if c.status >= 400 && err == nil {
				t.Error("HTTP error was ignored")
			}
			if c.body != "" && err != nil {
				t.Error(err)
			}
			if calls != c.wantCalls || registers != c.wantCalls {
				t.Fatalf("calls=%d registers=%d, want %d each", calls, registers, c.wantCalls)
			}
		})
	}
}

func TestSignedJSONRegistrationFailure(t *testing.T) {
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
		if _, err := signedJSON(context.Background(), videoAPI, []byte(`{}`)); err == nil {
			t.Fatal("expected registration failure")
		}
	}
	if calls != 1 {
		t.Fatalf("registration calls=%d, want 1 during cooldown", calls)
	}

	deviceRetry = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := signedJSON(ctx, videoAPI, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 || !deviceRetry.IsZero() {
		t.Fatal("cancelled caller sent a request or imposed a cooldown")
	}
}
