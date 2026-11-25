package hongguo

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/beilunyang/openhongguo/adapters/filestore"
	"github.com/beilunyang/openhongguo/adapters/memstore"
	"github.com/beilunyang/openhongguo/sdk"
	"github.com/beilunyang/openhongguo/sdk/model"
)

// HotQuery is a trending search term or drama surfaced by the HotSearch API.
type HotQuery struct {
	Title    string
	SeriesID string // optional; present for trending drama items
	Score    string // e.g. "355万热搜值"
}

// SignerConfig holds optional openhongguo-signer endpoint settings.
type SignerConfig struct {
	URL   string
	Token string
}

var (
	ohgMu          sync.Mutex
	ohgClient      *sdk.Client
	ohgProfilePath string
	ohgSignerURL   string
	ohgSignerToken string
	// ohgBackupEnabled allows unit tests that assert exact built-in retry counts
	// to isolate the built-in pipeline without disabling production fallback.
	ohgBackupEnabled = true
	// ohgTransportOverride lets tests inject a mock sdk.Transport.
	ohgTransportOverride sdk.Transport
)

// SetOpenhongguoProfileFile sets where openhongguo persists its anonymous
// device profile JSON. Empty uses in-memory storage.
func SetOpenhongguoProfileFile(path string) {
	ohgMu.Lock()
	defer ohgMu.Unlock()
	if ohgProfilePath != path {
		ohgProfilePath = path
		ohgClient = nil
	}
}

// SetSignerConfig configures an optional external openhongguo-signer service
// (compatible with POST /feature-hash). Passing an empty URL disables remote
// signing and reverts to built-in pure Go signing.
func SetSignerConfig(rawURL, token string) error {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed != "" {
		if err := validateSignerURL(trimmed); err != nil {
			return err
		}
	}
	ohgMu.Lock()
	ohgSignerURL = trimmed
	ohgSignerToken = strings.TrimSpace(token)
	ohgClient = nil
	ohgMu.Unlock()
	return nil
}

// CurrentSignerConfig returns the active remote signer settings.
func CurrentSignerConfig() SignerConfig {
	ohgMu.Lock()
	defer ohgMu.Unlock()
	return SignerConfig{URL: ohgSignerURL, Token: ohgSignerToken}
}

func validateSignerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("签名服务地址格式无效")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return errors.New("远程签名服务必须使用 HTTPS（本机地址除外）")
}

func resetOpenhongguoClient() {
	ohgMu.Lock()
	ohgClient = nil
	ohgMu.Unlock()
}

// directSDKTransport adapts smovie's directTransport httpClient to sdk.Transport,
// keeping VPN bypass and enforcing anonymous-only header boundaries.
type directSDKTransport struct {
	client *http.Client
}

func (t *directSDKTransport) Do(ctx context.Context, req *sdk.Request) (*sdk.Response, error) {
	if req == nil {
		return nil, errors.New("openhongguo transport: nil request")
	}
	for name, value := range req.Headers {
		if strings.EqualFold(name, "Authorization") || (strings.EqualFold(name, "Cookie") && !ohgGuestCookieAllowed(value)) {
			return nil, fmt.Errorf("%w: credential header refused", sdk.ErrAuthenticationRequired)
		}
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	hreq, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}
	client := t.client
	if client == nil {
		client = httpClient
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for k, values := range resp.Header {
		if strings.EqualFold(k, "Set-Cookie") || strings.EqualFold(k, "Cookie") {
			continue
		}
		if len(values) > 0 {
			headers[k] = values[0]
		}
	}
	cookies := map[string]string{}
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "" {
			cookies[cookie.Name] = cookie.Value
		}
	}
	return &sdk.Response{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       body,
		Cookies:    cookies,
	}, nil
}

func ohgGuestCookieAllowed(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "session") || strings.Contains(lower, "odin_tt") {
		return false
	}
	return strings.Contains(value, "passport_csrf_token=;") &&
		strings.Contains(value, "install_id=") &&
		strings.Contains(value, "ttreq=")
}

func getOpenhongguoClient() (*sdk.Client, error) {
	ohgMu.Lock()
	defer ohgMu.Unlock()
	if ohgClient != nil {
		return ohgClient, nil
	}
	var store sdk.ProfileStore
	if ohgProfilePath != "" {
		store = filestore.New(ohgProfilePath)
	} else {
		store = memstore.New()
	}
	transport := ohgTransportOverride
	if transport == nil {
		transport = &directSDKTransport{}
	}
	prof := activeProfile()
	opts := sdk.Options{
		Transport:     transport,
		ProfileStore:  store,
		RetryAttempts: 2,
		DeviceResets:  sdk.Int(1),
		SignerURL:     ohgSignerURL,
		SignerToken:   ohgSignerToken,
	}
	if prof != nil {
		if prof.raw.Playback.OpenhongguoAPIOrigin != "" {
			opts.APIOrigin = prof.raw.Playback.OpenhongguoAPIOrigin
		}
		if prof.raw.Playback.OpenhongguoRegOrigin != "" {
			opts.RegisterOrigin = prof.raw.Playback.OpenhongguoRegOrigin
		}
	}
	c, err := sdk.NewClient(opts)
	if err != nil {
		return nil, err
	}
	ohgClient = c
	return c, nil
}

var remoteSignatureHeaders = []string{"x-gorgon", "x-medusa", "x-helios", "x-khronos", "x-argus", "x-ladon"}

// applyRemoteSigner replaces signature headers on a built-in signed request
// when the user configured an external openhongguo-signer endpoint.
func applyRemoteSigner(ctx context.Context, req *http.Request, body []byte) error {
	cfg := CurrentSignerConfig()
	if cfg.URL == "" || req == nil {
		return nil
	}
	var payload bytes.Buffer
	enc := json.NewEncoder(&payload)
	enc.SetEscapeHTML(false)
	in := map[string]string{"url": req.URL.String()}
	if len(body) > 0 {
		in["body_hex"] = hex.EncodeToString(body)
	}
	if err := enc.Encode(in); err != nil {
		return err
	}
	sreq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL+"/feature-hash", &payload)
	if err != nil {
		return fmt.Errorf("签名服务请求无效：%w", err)
	}
	sreq.Header.Set("Content-Type", "application/json")
	sreq.Header.Set("Accept-Encoding", "identity")
	if cfg.Token != "" {
		sreq.Header.Set("X-Signer-Token", cfg.Token)
	}
	res, err := httpClient.Do(sreq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("签名服务不可用：%w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取签名服务响应失败：%w", err)
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return fmt.Errorf("签名服务返回错误：%s", e.Error)
		}
		return fmt.Errorf("签名服务返回 HTTP %d", res.StatusCode)
	}
	var out struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return errors.New("签名服务响应格式无效")
	}
	got := make(map[string]string, len(out.Headers))
	for k, v := range out.Headers {
		got[strings.ToLower(k)] = v
	}
	if got["x-medusa"] == "" {
		return errors.New("签名服务未返回有效签名")
	}
	for _, h := range remoteSignatureHeaders {
		req.Header.Del(h)
	}
	for _, h := range remoteSignatureHeaders {
		if v := got[h]; v != "" {
			req.Header.Set(h, v)
		}
	}
	return nil
}

// resolvePlayViaOpenhongguo resolves an episode using openhongguo/sdk as a
// resilient backup engine while preserving smovie's codec filtering, quality
// selection, and media domain allowlist.
func resolvePlayViaOpenhongguo(ctx context.Context, vid string, maxPx int) (*PlayInfo, error) {
	c, err := getOpenhongguoClient()
	if err != nil {
		return nil, err
	}
	pb, err := c.ResolvePlayback(ctx, vid, sdk.PlaybackOptions{})
	if err != nil && !errors.Is(err, sdk.ErrDRMProtected) {
		return nil, err
	}
	if pb == nil || len(pb.Streams) == 0 {
		return nil, errors.New("openhongguo 未返回可用片源")
	}
	contentKey := ""
	if pb.Protected {
		k, keyErr := sdk.ContentKey(pb)
		if keyErr != nil {
			return nil, fmt.Errorf("解密密钥派生失败：%w", keyErr)
		}
		contentKey = k.Hex
	}
	return selectOpenhongguoStream(vid, pb, contentKey, maxPx)
}

var digitsPattern = regexp.MustCompile(`\d+`)

func selectOpenhongguoStream(vid string, pb *model.Playback, contentKey string, maxPx int) (*PlayInfo, error) {
	type cand struct {
		stream model.Stream
		codec  string
		rank   int
		pixels int
		w, h   int
	}
	var cands []cand
	for _, s := range pb.Streams {
		if s.HLS {
			continue
		}
		codec, rank := compatibleCodecString(s.Codec)
		if rank == 2 {
			continue
		}
		w, h := s.Width, s.Height
		px := min(w, h)
		if px == 0 {
			px, _ = strconv.Atoi(digitsPattern.FindString(firstNonEmpty(s.Definition, s.Quality)))
		}
		cands = append(cands, cand{stream: s, codec: codec, rank: rank, pixels: px, w: w, h: h})
	}
	if len(cands) == 0 {
		return nil, errors.New("本集暂未提供兼容的 H.264/HEVC 片源")
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].pixels != cands[j].pixels {
			return cands[i].pixels > cands[j].pixels
		}
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		return cands[i].stream.Bitrate > cands[j].stream.Bitrate
	})
	var qualities []int
	for _, c := range cands {
		if c.pixels > 0 && (len(qualities) == 0 || qualities[len(qualities)-1] != c.pixels) {
			qualities = append(qualities, c.pixels)
		}
	}
	if maxPx > 0 {
		var fit []cand
		for _, c := range cands {
			if c.pixels <= maxPx {
				fit = append(fit, c)
			}
		}
		if len(fit) == 0 {
			low := cands[len(cands)-1].pixels
			for _, c := range cands {
				if c.pixels == low {
					fit = append(fit, c)
				}
			}
		}
		cands = fit
	}
	audioURL := ""
	if rawAudio := cleanURL(pb.AudioURL); rawAudio != "" {
		if checkMediaURL(rawAudio) == nil {
			audioURL = rawAudio
		}
	}
	ua := activeProfile().raw.Playback.PlaybackUA
	var lastErr error
	for _, c := range cands {
		var urls []string
		seen := map[string]bool{}
		for _, raw := range []string{c.stream.URL, c.stream.BackupURL} {
			u := cleanURL(raw)
			if u == "" || seen[u] {
				continue
			}
			if err := checkMediaURL(u); err != nil {
				lastErr = err
				continue
			}
			seen[u] = true
			urls = append(urls, u)
		}
		if len(urls) == 0 {
			continue
		}
		codec := c.codec
		if codec == "bytevc1" {
			codec = "hevc"
		}
		quality := ""
		if c.pixels > 0 {
			quality = strconv.Itoa(c.pixels) + "P"
		}
		return &PlayInfo{
			VID:        vid,
			URL:        urls[0],
			BackupURLs: urls[1:],
			AudioURL:   audioURL,
			ContentKey: contentKey,
			Codec:      codec,
			Quality:    quality,
			UserAgent:  ua,
			Width:      c.w,
			Height:     c.h,
			Qualities:  qualities,
		}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("本集片源地址无法解析")
	}
	return nil, lastErr
}

func compatibleCodecString(raw string) (string, int) {
	codec := strings.ToLower(strings.TrimSpace(raw))
	switch codec {
	case "h264", "avc", "avc1":
		return codec, 0
	case "hevc", "h265", "hvc1", "hev1", "bytevc1":
		return codec, 1
	default:
		return codec, 2
	}
}

// seasonsViaOpenhongguo fetches all seasons of an album via openhongguo,
// preferring GetAlbum when albumID is valid and falling back to Seasons(seed.ID)
// when GetAlbum reports an album as unavailable.
func seasonsViaOpenhongguo(ctx context.Context, seed Series, albumID string) ([]Season, error) {
	c, err := getOpenhongguoClient()
	if err != nil {
		return nil, err
	}
	album, err := c.GetAlbum(ctx, albumID, sdk.AlbumOptions{})
	if (err != nil || album == nil || len(album.Seasons) == 0) && seriesIDPattern.MatchString(seed.ID) {
		if alt, altErr := c.Seasons(ctx, seed.ID); altErr == nil && alt != nil && len(alt.Seasons) > 0 {
			album, err = alt, nil
		}
	}
	if err != nil || album == nil || len(album.Seasons) == 0 {
		if err == nil {
			err = errors.New("合集接口未返回分季")
		}
		return nil, err
	}
	out := make([]Season, 0, len(album.Seasons))
	seen := map[string]bool{}
	for i, ms := range album.Seasons {
		if !seriesIDPattern.MatchString(ms.SeriesID) || seen[ms.SeriesID] {
			continue
		}
		seen[ms.SeriesID] = true
		idx := ms.Index
		if idx <= 0 {
			idx = seasonIndex(ms.Title)
		}
		if idx <= 0 {
			idx = i + 1
		}
		cover := ohgSafeCoverURL(ms.CoverURL)
		title := strings.TrimSpace(ms.Title)
		epCount := ms.EpisodeCount
		epLabel := ""
		if epCount > 0 {
			epLabel = fmt.Sprintf("全%d集", epCount)
		}
		if ms.SeriesID == seed.ID {
			if cover == "" || strings.Contains(strings.ToLower(cover), ".heic") {
				cover = seed.Cover
			}
			if title == "" {
				title = seed.Title
			}
			if epCount == 0 {
				epCount = seed.EpisodeCount
			}
			if epLabel == "" {
				epLabel = seed.EpisodeLabel
			}
		}
		out = append(out, Season{
			Series: Series{
				ID:           ms.SeriesID,
				Title:        title,
				Cover:        cover,
				EpisodeCount: epCount,
				EpisodeLabel: epLabel,
			},
			Index: idx,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("合集接口未返回有效分季")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func ohgSafeCoverURL(raw string) string {
	u := imageURL(raw)
	if u == "" || strings.Contains(strings.ToLower(u), ".heic") {
		return ""
	}
	return u
}

// Recommend fetches server-side related dramas for seriesID via openhongguo.
func Recommend(ctx context.Context, seriesID string) ([]Series, error) {
	if !seriesIDPattern.MatchString(seriesID) {
		return nil, errors.New("剧集编号无效")
	}
	c, err := getOpenhongguoClient()
	if err != nil {
		return nil, err
	}
	rec, err := c.SeriesRecommend(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if rec == nil || len(rec.Items) == 0 {
		return nil, nil
	}
	out := make([]Series, 0, len(rec.Items))
	seen := map[string]bool{seriesID: true}
	for _, it := range rec.Items {
		if !seriesIDPattern.MatchString(it.ID) || strings.TrimSpace(it.Title) == "" || seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		label := ""
		if it.EpisodeCount > 0 {
			label = fmt.Sprintf("全%d集", it.EpisodeCount)
		}
		var tags []string
		if cat := strings.TrimSpace(it.Category); cat != "" {
			tags = []string{cat}
		}
		out = append(out, Series{
			ID:           it.ID,
			Title:        strings.TrimSpace(it.Title),
			Cover:        ohgSafeCoverURL(it.CoverURL),
			Intro:        strings.TrimSpace(it.Description),
			Tags:         tags,
			EpisodeCount: it.EpisodeCount,
			EpisodeLabel: label,
		})
	}
	return out, nil
}

// HotSearch loads trending search queries from the App API via openhongguo.
func HotSearch(ctx context.Context) ([]HotQuery, error) {
	c, err := getOpenhongguoClient()
	if err != nil {
		return nil, err
	}
	hs, err := c.HotSearch(ctx)
	if err != nil {
		return nil, err
	}
	if hs == nil {
		return nil, nil
	}
	var out []HotQuery
	seen := map[string]bool{}
	add := func(title, id, score string) {
		t := strings.TrimSpace(title)
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, HotQuery{Title: t, SeriesID: id, Score: strings.TrimSpace(score)})
	}
	for _, g := range hs.Guesses {
		add(g.Title, g.SeriesID, g.SearchScore)
	}
	for _, b := range hs.Boards {
		for _, it := range b.Items {
			add(it.Title, it.SeriesID, it.SearchScore)
		}
	}
	return out, nil
}

// Suggest returns search autocomplete terms for prefix via openhongguo.
func Suggest(ctx context.Context, prefix string) ([]string, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, nil
	}
	c, err := getOpenhongguoClient()
	if err != nil {
		return nil, err
	}
	items, err := c.Suggest(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		t := strings.TrimSpace(it.Text)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}

func isOpenhongguoBackupEnabled() bool {
	ohgMu.Lock()
	defer ohgMu.Unlock()
	return ohgBackupEnabled
}
