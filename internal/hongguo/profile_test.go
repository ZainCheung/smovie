package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signTestProfile(t *testing.T, priv ed25519.PrivateKey, raw []byte) []byte {
	t.Helper()
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, raw)))
}

func TestProtocolRefreshVerifyCacheAndReset(t *testing.T) {
	isolatePlaybackSession(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	var p ProtocolProfile
	if err := json.Unmarshal(embeddedProfileJSON, &p); err != nil {
		t.Fatal(err)
	}
	p.ProfileVersion = "2026.11.25-hotfix"
	p.MinAppVersion = "0.1.10"
	p.MaxAppVersion = "0.9.99"
	p.Playback.RouteOrder = []string{"v5_full", "openhongguo"}
	p.Playback.FullDeviceLevel = 1
	p.Playback.MedusaV5.FullField21Status = 312
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	sig := signTestProfile(t, priv, raw)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/protocol-profile.json":
			_, _ = w.Write(raw)
		case "/protocol-profile.json.sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	InitProtocol(ProtocolOptions{
		CacheDir:   cacheDir,
		AppVersion: "0.1.16",
		RemoteURL:  srv.URL + "/protocol-profile.json",
		PublicKey:  pubB64,
		HTTPClient: srv.Client(),
	})
	if st := CurrentProtocolStatus(); st.Source != "embedded" {
		t.Fatalf("expected embedded baseline, got %+v", st)
	}

	st, err := RefreshProtocol(context.Background())
	if err != nil {
		t.Fatalf("RefreshProtocol failed: %v", err)
	}
	if st.Source != "remote" || st.Version != "2026.11.25-hotfix" {
		t.Fatalf("unexpected status after refresh: %+v", st)
	}
	if activeProfile().raw.Playback.FullDeviceLevel != 1 || activeProfile().raw.Playback.MedusaV5.FullField21Status != 312 {
		t.Fatal("hotfix values not activated")
	}

	// Verify signedFullMobileRequest actually honors the hotfixed device_level.
	dev := &device{DeviceID: "1234567890123456789", IID: "2345678901234567890", CDID: "synthetic"}
	req, err := signedFullMobileRequest(context.Background(), mobileVideoPayload("123"), dev, time.Unix(1800000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, _ := io.ReadAll(req.Body)
	zr, err := gzip.NewReader(bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Biz struct {
			DeviceLevel int `json:"device_level"`
		} `json:"biz_param"`
	}
	_ = json.NewDecoder(zr).Decode(&decoded)
	_ = zr.Close()
	if decoded.Biz.DeviceLevel != 1 {
		t.Fatalf("expected hotfixed device_level=1, got %d", decoded.Biz.DeviceLevel)
	}

	// Simulate app restart: InitProtocol reloads the verified cached profile.
	base, _ := compileProfile(embeddedProfileJSON, "", "embedded", time.Time{})
	currentProfile.Store(base)
	InitProtocol(ProtocolOptions{
		CacheDir:   cacheDir,
		AppVersion: "0.1.16",
		RemoteURL:  srv.URL + "/protocol-profile.json",
		PublicKey:  pubB64,
		HTTPClient: srv.Client(),
	})
	if st := CurrentProtocolStatus(); st.Source != "cached" || st.Version != "2026.11.25-hotfix" {
		t.Fatalf("expected cached profile on restart, got %+v", st)
	}

	// ResetProtocol removes cache and restores embedded baseline.
	reset := ResetProtocol()
	if reset.Source != "embedded" || reset.Version == "2026.11.25-hotfix" {
		t.Fatalf("expected embedded baseline after reset, got %+v", reset)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "protocol-profile.json")); !os.IsNotExist(err) {
		t.Fatal("cache file not removed on reset")
	}
}

func TestProtocolSecurityAndVersionBoundaries(t *testing.T) {
	isolatePlaybackSession(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	t.Run("tampered signature rejected", func(t *testing.T) {
		raw := append([]byte(nil), embeddedProfileJSON...)
		sig := signTestProfile(t, priv, []byte("different-payload"))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".sig") {
				_, _ = w.Write(sig)
				return
			}
			_, _ = w.Write(raw)
		}))
		defer srv.Close()
		InitProtocol(ProtocolOptions{CacheDir: t.TempDir(), RemoteURL: srv.URL + "/p.json", PublicKey: pubB64, HTTPClient: srv.Client()})
		if _, err := RefreshProtocol(context.Background()); err == nil || !strings.Contains(err.Error(), "签名") {
			t.Fatalf("expected signature rejection, got %v", err)
		}
	})

	t.Run("untrusted domains rejected (SSRF protection)", func(t *testing.T) {
		cases := []func(*ProtocolProfile){
			func(p *ProtocolProfile) { p.Playback.AppAPIBase = "https://attacker.example.com" },
			func(p *ProtocolProfile) { p.Playback.RegisterURL = "http://log5-applog-lf.fqnovel.com/service/2/device_register/" },
			func(p *ProtocolProfile) { p.Playback.FallbackHost = "127.0.0.1" },
			func(p *ProtocolProfile) { p.Playback.AllowedMediaDomains = []string{"attacker.example.com"} },
			func(p *ProtocolProfile) { p.Playback.AllowedVideoInfoDomains = []string{"snssdk.com.evil.test"} },
			func(p *ProtocolProfile) { p.Catalog.SiteOrigin = "https://phishing.test" },
			func(p *ProtocolProfile) { p.Danmaku.CommentAPI = "https://evil.test/comment" },
			func(p *ProtocolProfile) { p.Playback.RouteOrder = []string{"arbitrary_exec"} },
			func(p *ProtocolProfile) { p.Playback.MedusaV5.MagicHex = "abcd" },
			func(p *ProtocolProfile) { p.SchemaVersion = 99 },
		}
		for i, mutate := range cases {
			var p ProtocolProfile
			_ = json.Unmarshal(embeddedProfileJSON, &p)
			mutate(&p)
			raw, _ := json.Marshal(p)
			if _, err := ValidateProfileJSON(raw, "0.1.16"); err == nil {
				t.Fatalf("case %d: invalid profile accepted", i)
			}
		}
	})

	t.Run("app version range enforced", func(t *testing.T) {
		var p ProtocolProfile
		_ = json.Unmarshal(embeddedProfileJSON, &p)
		p.MinAppVersion = "0.2.0"
		raw, _ := json.Marshal(p)
		if _, err := ValidateProfileJSON(raw, "0.1.15"); err == nil {
			t.Fatal("min_app_version not enforced")
		}
		if _, err := ValidateProfileJSON(raw, "0.2.1"); err != nil {
			t.Fatalf("compatible version rejected: %v", err)
		}
	})
}

func TestResolvePlayHonorsHotfixRouteOrderV5FullFirst(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}

	var p ProtocolProfile
	_ = json.Unmarshal(embeddedProfileJSON, &p)
	p.Playback.RouteOrder = []string{"v5_full"}
	raw, _ := json.Marshal(p)
	compiled, err := compileProfile(raw, "0.1.16", "remote", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	currentProfile.Store(compiled)

	fullCalls, otherCalls := 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		if strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
			fullCalls++
			return jsonResponse(`{"code":0,"data":{"1004":{"123":{"video_model":{"video_list":[{"codec_type":"h264","width":1920,"height":1080,"main_url":"https://example.fqnovelvod.com/full-first.mp4"}]}}}}}`), nil
		}
		otherCalls++
		return jsonResponse(""), nil
	})}

	info, err := ResolvePlay(context.Background(), "123", 0)
	if err != nil {
		t.Fatalf("ResolvePlay failed: %v", err)
	}
	if fullCalls != 1 || otherCalls != 0 || info.URL != "https://example.fqnovelvod.com/full-first.mp4" {
		t.Fatalf("expected direct v5_full route: full=%d other=%d url=%s", fullCalls, otherCalls, info.URL)
	}
}

func TestResolvePlaySingleVideoModelV5PrimaryAndFlatParse(t *testing.T) {
	isolatePlaybackSession(t)
	oldClient, oldRetry := httpClient, deviceRetry
	t.Cleanup(func() { httpClient, deviceRetry = oldClient, oldRetry })
	deviceRetry = time.Time{}

	base, err := compileProfile(embeddedProfileJSON, "0.1.16", "embedded", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	currentProfile.Store(base)

	registers, singleCalls := 0, 0
	httpClient = &http.Client{Transport: signingTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/service/2/device_register/" {
			registers++
			return jsonResponse(`{"device_id_str":"1234567890123456789","install_id_str":"2345678901234567890"}`), nil
		}
		if req.URL.Path != "/novel/player/video_model/v1/" {
			t.Fatalf("expected primary video_model_v5 route, got %s", req.URL.Path)
		}
		if req.Header.Get("x-medusa") == "" || req.Header.Get("x-argus") == "" || req.Header.Get("x-ladon") == "" || !strings.HasPrefix(req.Header.Get("x-gorgon"), "8404") {
			t.Fatal("video_model_v5 missing required V5 Medusa + 551 Argus/Ladon/Gorgon headers")
		}
		singleCalls++
		return jsonResponse(`{"code":0,"data":{"expire_time":1795611418,"video_model":"{\"video_id\":\"v1\",\"video_duration\":60,\"video_list\":[{\"codec_type\":\"h265\",\"vwidth\":1080,\"vheight\":1920,\"main_url\":\"https://example.fqnovelvod.com/single-v5.mp4\"}]}"}}`), nil
	})}

	for i := 0; i < 2; i++ {
		info, err := ResolvePlay(context.Background(), "123", 0)
		if err != nil {
			t.Fatalf("attempt %d failed: %v", i, err)
		}
		if info.URL != "https://example.fqnovelvod.com/single-v5.mp4" || info.Codec != "h265" || info.Quality != "1080P" {
			t.Fatalf("unexpected info: %+v", info)
		}
	}
	if registers != 1 || singleCalls != 2 {
		t.Fatalf("expected single registration + reused identity: registers=%d calls=%d", registers, singleCalls)
	}
}
