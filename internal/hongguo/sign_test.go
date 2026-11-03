package hongguo

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
		TS                                       uint32
		Query, Body, Gorgon, Helios, F13, Medusa string
	}
	if err := json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for i, v := range vectors {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			bh := md5.Sum([]byte(v.Body))
			qh := sm3([]byte(v.Query))
			if branch := signingBranch(qh, bh[:], v.TS); branch != 0 {
				t.Fatalf("fixture uses branch %d", branch)
			}
			if got := xGorgon(v.Query, []byte(v.Body), v.TS, 0x1234); got != v.Gorgon {
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
			got := buildMedusa(v.Query, []byte(v.Body), v.TS, &device{DeviceID: "1234567890123456789"}, "fixed-signing-process-nonce", 1793606400123, next)
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
	oldClient, oldDev, oldRetry := httpClient, cachedDev, deviceRetry
	t.Cleanup(func() { httpClient, cachedDev, deviceRetry = oldClient, oldDev, oldRetry })
	cachedDev = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "original-device"}
	deviceRetry = time.Time{}
	playCalls, registerCalls := 0, 0
	var payloads [][]byte
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		respond := func(s string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(s))}, nil
		}
		if req.URL.Path == "/service/2/device_register/" {
			registerCalls++
			return respond(`{"device_id_str":"3456789012345678901","install_id_str":"4567890123456789012"}`)
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
		for _, h := range []string{"x-ss-req-ticket", "sdk-version", "x-khronos", "x-gorgon", "x-ladon", "x-argus", "x-helios", "x-medusa", "x-tt-store-region", "x-tt-store-region-src"} {
			if req.Header.Get(h) == "" {
				t.Errorf("play request %d missing %s", playCalls, h)
			}
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
		if playCalls == 1 {
			return respond("")
		}
		return respond(`{"code":0,"data":{"123":{"video_model":{"video_list":[{"main_url":"https://example.fqnovelvod.com/video.mp4","codec_type":"h264","width":720,"height":1280}]}}}}`)
	})}
	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if playCalls != 2 || registerCalls != 1 {
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
	oldClient, oldDev := httpClient, cachedDev
	t.Cleanup(func() { httpClient, cachedDev = oldClient, oldDev })
	cachedDev = &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "original-device"}
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
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
