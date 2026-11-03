package hongguo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	registerURL = "https://log5-applog-lf.fqnovel.com/service/2/device_register/?tt_data=a&cronet_version=8d40f833_2026-03-03&ttnet_version=4.2.243.31-douyin&use_store_region_cookie=1"
	videoAPI    = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/"
	videoUA     = "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
	playbackUA  = "com.phoenix.read/71532 (Linux; U; Android 9; SM-N9860; Build/PQ3A.190705.10241111;tt-ok/3.12.13.20)"
	registerUA  = "com.phoenix.read/72232 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3)"
)

var kdfSalt, _ = hex.DecodeString("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")

var deviceQuery = map[string]string{
	"ac": "wifi", "channel": "update_64", "aid": "8662", "app_name": "novelread",
	"version_code": "71332", "version_name": "7.1.3.32", "device_platform": "android", "os": "android",
	"ssmix": "a", "device_type": "25053RT47C", "device_brand": "Redmi", "language": "zh", "os_api": "36",
	"os_version": "16", "manifest_version_code": "71332", "resolution": "1280*2772", "dpi": "520",
	"update_version_code": "71332", "host_abi": "arm64-v8a", "dragon_device_type": "phone", "pv_player": "71332",
	"compliance_status": "0", "need_personal_recommend": "1", "player_so_load": "1", "is_android_pad_screen": "0",
}

// device is an anonymous playback identity, registered once per process.
type device struct{ DeviceID, IID, CDID string }

var (
	deviceMu    sync.Mutex
	cachedDev   *device
	deviceRetry time.Time
)

func getDevice(ctx context.Context, renew bool) (*device, error) {
	deviceMu.Lock()
	defer deviceMu.Unlock()
	if cachedDev != nil && !renew {
		return cachedDev, nil
	}
	if time.Now().Before(deviceRetry) {
		return nil, errors.New("匿名播放设备注册失败，请稍后重试")
	}
	d, err := registerDevice(ctx)
	if err != nil {
		deviceRetry = time.Now().Add(10 * time.Second)
		return nil, err
	}
	cachedDev = d
	return d, nil
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func registerDevice(ctx context.Context) (*device, error) {
	now := time.Now().UnixMilli()
	cdid := randomUUID()
	header := map[string]any{
		"display_name": "红果免费短剧", "update_version_code": 73932, "manifest_version_code": 73932,
		"app_version_minor": "7.3.9.32", "aid": 8662, "channel": "update_64", "package": "com.phoenix.read",
		"app_version": "7.3.9.32", "version_code": 73932, "sdk_version": "3.7.0-rc.34-fanqie-xiaoshuo",
		"sdk_target_version": 29, "git_hash": "3a88f4e", "os": "Android", "os_version": "16", "os_api": 36,
		"device_model": "25053RT47C", "device_brand": "Redmi", "device_manufacturer": "Redmi",
		"cpu_abi": "arm64-v8a", "release_build": "b5e187f", "density_dpi": 520, "display_density": "mdpi",
		"resolution": "1280x2772", "language": "zh", "timezone": 8, "access": "wifi", "not_request_sender": 0,
		"carrier": "CHINA MOBILE", "mcc_mnc": "46000", "rom": "eng.root.20231116.160340",
		"rom_version": "W528JS release-keys", "cdid": cdid,
		"sig_hash": "56a962410c494bbaf0b58dba20cae56f", "openudid": randomHex(8),
		"clientudid": randomUUID(), "ipv6_list": []any{}, "region": "CN", "tz_name": "Asia/Shanghai",
		"tz_offset": 28800, "sim_region": "cn", "sim_serial_number": []any{}, "oaid_may_support": false,
		"req_id": randomUUID(), "device_platform": "android",
		"custom":                map[string]any{"account_region_src": "fallback", "account_region": "cn", "host_bit": 64, "dragon_device_type": "phone"},
		"pre_installed_channel": "", "apk_first_install_time": now, "is_system_app": 0, "sdk_flavor": "china", "guest_mode": 0,
	}
	payload, _ := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": now})
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(payload)
	_ = zw.Close()
	compressed := gz.Bytes()

	random := make([]byte, 32)
	_, _ = rand.Read(random)
	h1 := sha512.Sum512(random)
	derived := sha512.Sum512(append(h1[:], kdfSalt...))
	block, _ := aes.NewCipher(derived[:16])
	digest := sha512.Sum512(compressed)
	plain := pkcs7(append(digest[:], compressed...))
	enc := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, derived[16:32]).CryptBlocks(enc, plain)
	prefix, _ := hex.DecodeString("746305100000")
	data := append(append(prefix, random...), enc...)

	stub := md5.Sum(data)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(data))
	for k, v := range map[string]string{
		"User-Agent": registerUA, "Accept": "application/json", "Content-Type": "application/octet-stream;tt-data=a",
		"log-encode-type": "gzip", "x-ss-dp": "8662", "x-ss-req-ticket": strconv.FormatInt(now, 10),
		"x-vc-bdturing-sdk-version": "4.0.3.cn", "lc": "101", "sdk-version": "2",
		"passport-sdk-version": "5051452", "x-neptune": "-8|50:51:59:30:40:47:49:39",
		"x-ss-stub": strings.ToUpper(hex.EncodeToString(stub[:])),
	} {
		req.Header.Set(k, v)
	}
	body, err := doJSON(req)
	if err != nil {
		return nil, fmt.Errorf("匿名播放设备注册失败：%w", err)
	}
	var res map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil {
		return nil, errors.New("匿名播放设备注册失败：响应无效")
	}
	id := func(name string) string {
		for _, k := range []string{name + "_str", name} {
			switch v := res[k].(type) {
			case string:
				if vidPattern.MatchString(v) && strings.Trim(v, "0") != "" {
					return v
				}
			case json.Number:
				if s := v.String(); vidPattern.MatchString(s) && strings.Trim(s, "0") != "" {
					return s
				}
			}
		}
		return ""
	}
	d := &device{DeviceID: id("device_id"), IID: id("install_id"), CDID: cdid}
	if d.DeviceID == "" || d.IID == "" {
		return nil, errors.New("匿名播放设备注册失败：未返回设备编号")
	}
	return d, nil
}

func pkcs7(b []byte) []byte {
	n := aes.BlockSize - len(b)%aes.BlockSize
	return append(b, bytes.Repeat([]byte{byte(n)}, n)...)
}

var errEmpty = errors.New("接口返回空响应")

func doJSON(req *http.Request) ([]byte, error) {
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errEmpty
	}
	return body, nil
}

// PlayInfo is where an episode's media is and how to decrypt it.
type PlayInfo struct {
	VID        string
	URL        string
	ContentKey string // hex AES-128 key for CENC, empty when not encrypted
	Codec      string
	Quality    string
	UserAgent  string
	// Width and Height of the picture, when the API tells them.
	Width, Height int
	// Qualities are the episode's resolutions, as the shorter side in
	// pixels, highest first.
	Qualities []int
}

// ResolvePlay asks the app API for an episode's media, of the highest
// resolution up to maxPx (the shorter side in pixels; 0 for any), or of
// the lowest when every one is higher.
func ResolvePlay(ctx context.Context, vid string, maxPx int) (*PlayInfo, error) {
	if !vidPattern.MatchString(vid) {
		return nil, errors.New("分集编号无效")
	}
	payload, _ := json.Marshal(map[string]any{
		"biz_param": map[string]any{"detail_page_version": 0, "device_level": 3, "disable_digg_stat": false,
			"need_all_video_definition": true, "need_mp4_align": false, "use_os_player": false,
			"use_server_dns": false, "video_platform": 1024},
		"mixed_video_id_map": map[string]any{"1004": []string{vid}},
	})
	dev, err := getDevice(ctx, false)
	if err != nil {
		return nil, err
	}
	var body []byte
	for attempt := 0; attempt < 2; attempt++ {
		req, signErr := playbackRequest(ctx, payload, dev, time.Now())
		if signErr != nil {
			return nil, signErr
		}
		body, err = doJSON(req)
		if err == nil {
			break
		}
		if attempt == 0 && errors.Is(err, errEmpty) {
			if dev, err = getDevice(ctx, true); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("取源失败：%w", err)
	}

	var res struct {
		Code json.Number                `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, errors.New("播放接口返回无效数据")
	}
	if c := res.Code.String(); c != "" && c != "0" {
		return nil, errors.New("接口暂未提供本集播放信息")
	}
	var entry struct {
		VideoModel json.RawMessage `json:"video_model"`
	}
	if err := json.Unmarshal(res.Data[vid], &entry); err != nil || len(entry.VideoModel) == 0 {
		return nil, errors.New("接口未返回本集片源")
	}
	modelJSON := []byte(entry.VideoModel)
	var s string
	if json.Unmarshal(modelJSON, &s) == nil {
		modelJSON = []byte(s)
	}
	var model videoInfo
	if err := json.Unmarshal(modelJSON, &model); err != nil {
		return nil, errors.New("片源数据无法解析")
	}
	rows := model.rows()
	inline := len(rows) > 0
	info := model
	ua := playbackUA
	if !inline {
		fallback := parseFallback(model.FallbackAPI)
		if fallback == "" {
			return nil, errors.New("接口未返回本集片源地址")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fallback, nil)
		req.Header.Set("User-Agent", videoUA)
		fb, err := doJSON(req)
		if err != nil {
			return nil, fmt.Errorf("取源失败：%w", err)
		}
		var outer struct {
			VideoInfo struct {
				Data videoInfo `json:"data"`
			} `json:"video_info"`
		}
		if err := json.Unmarshal(fb, &outer); err != nil {
			return nil, errors.New("片源数据无法解析")
		}
		info = outer.VideoInfo.Data
		rows = info.rows()
		ua = videoUA
	}
	type cand struct {
		item   videoItem
		codec  string
		rank   int
		pixels int
		w, h   int
	}
	var cands []cand
	for _, it := range rows {
		codec := strings.ToLower(firstNonEmpty(it.CodecType, it.VideoMeta.CodecType))
		rank := 2
		switch codec {
		case "h264", "avc", "avc1":
			rank = 0
		case "hevc", "h265", "hvc1", "hev1", "bytevc1":
			rank = 1
		}
		if rank == 2 {
			continue
		}
		w, h := it.size()
		px := min(w, h)
		if px == 0 {
			px, _ = strconv.Atoi(regexp.MustCompile(`\d+`).FindString(firstNonEmpty(it.QualityDesc, it.Definition, it.VideoMeta.Definition)))
		}
		cands = append(cands, cand{it, codec, rank, px, w, h})
	}
	if len(cands) == 0 {
		return nil, errors.New("本集暂未提供兼容的 H.264/HEVC 片源")
	}
	// Highest resolution first; H.264 before HEVC at the same resolution.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].pixels != cands[j].pixels {
			return cands[i].pixels > cands[j].pixels
		}
		return cands[i].rank < cands[j].rank
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
		if len(fit) == 0 { // all higher: the lowest
			low := cands[len(cands)-1].pixels
			for _, c := range cands {
				if c.pixels == low {
					fit = append(fit, c)
				}
			}
		}
		cands = fit
	}
	var lastErr error
	for _, c := range cands {
		spade := firstNonEmpty(c.item.SpadeA, c.item.EncryptInfo.SpadeA)
		key := ""
		if spade != "" {
			k, err := deriveContentKey(spade)
			if err != nil {
				lastErr = err
				continue
			}
			key = hex.EncodeToString(k)
		}
		for _, raw := range append([]string{c.item.MainURL, c.item.BackupURL}, c.item.BackupURLList...) {
			if raw == "" {
				continue
			}
			media := cleanURL(raw)
			if info.KeySeed != "" {
				seed, err := base64.StdEncoding.DecodeString(info.KeySeed)
				if err != nil {
					lastErr = err
					continue
				}
				if media, err = decryptSpadeURL(raw, seed); err != nil {
					lastErr = err
					continue
				}
			}
			if err := checkMediaURL(media); err != nil {
				lastErr = err
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
			return &PlayInfo{VID: vid, URL: media, ContentKey: key, Codec: codec, Quality: quality, UserAgent: ua, Width: c.w, Height: c.h, Qualities: qualities}, nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("本集片源地址无法解析")
	}
	return nil, lastErr
}

type videoMeta struct {
	CodecType  string  `json:"codec_type"`
	Definition string  `json:"definition"`
	VWidth     flexInt `json:"vwidth"`
	VHeight    flexInt `json:"vheight"`
	Width      flexInt `json:"width"`
	Height     flexInt `json:"height"`
}

type videoItem struct {
	MainURL       string   `json:"main_url"`
	BackupURL     string   `json:"backup_url"`
	BackupURLList []string `json:"backup_url_list"`
	CodecType     string   `json:"codec_type"`
	QualityDesc   string   `json:"quality_desc"`
	Definition    string   `json:"definition"`
	SpadeA        string   `json:"spade_a"`
	EncryptInfo   struct {
		SpadeA string `json:"spade_a"`
	} `json:"encrypt_info"`
	VWidth    flexInt   `json:"vwidth"`
	VHeight   flexInt   `json:"vheight"`
	Width     flexInt   `json:"width"`
	Height    flexInt   `json:"height"`
	VideoMeta videoMeta `json:"video_meta"`
}

func (it videoItem) size() (int, int) {
	for _, p := range [][2]flexInt{{it.VWidth, it.VHeight}, {it.Width, it.Height}, {it.VideoMeta.VWidth, it.VideoMeta.VHeight}, {it.VideoMeta.Width, it.VideoMeta.Height}} {
		if p[0] > 0 && p[1] > 0 {
			return int(p[0]), int(p[1])
		}
	}
	return 0, 0
}

type videoInfo struct {
	VideoList   json.RawMessage `json:"video_list"`
	FallbackAPI json.RawMessage `json:"fallback_api"`
	KeySeed     string          `json:"key_seed"`
}

// rows reads video_list, an object keyed by rendition or an array.
func (v videoInfo) rows() []videoItem {
	var arr []videoItem
	if json.Unmarshal(v.VideoList, &arr) == nil {
		return arr
	}
	var obj map[string]videoItem
	if json.Unmarshal(v.VideoList, &obj) == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			arr = append(arr, obj[k])
		}
	}
	return arr
}

func parseFallback(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var inner struct {
			FallbackAPI string `json:"fallback_api"`
		}
		if json.Unmarshal([]byte(s), &inner) == nil && inner.FallbackAPI != "" {
			return inner.FallbackAPI
		}
		return strings.TrimSpace(s)
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil && len(arr) > 0 {
		return strings.TrimSpace(arr[0])
	}
	var obj struct {
		FallbackAPI string `json:"fallback_api"`
	}
	_ = json.Unmarshal(raw, &obj)
	return strings.TrimSpace(obj.FallbackAPI)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func checkMediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return errors.New("片源地址格式无效")
	}
	host := u.Hostname()
	for _, d := range []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return nil
		}
	}
	return errors.New("片源返回了不受支持的服务地址")
}

// deriveContentKey turns a spade_a into the media's AES-128 key.
func deriveContentKey(value string) ([]byte, error) {
	s := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimSpace(value))
	raw, err := base64.StdEncoding.DecodeString(padBase64(s))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return nil, errors.New("spade_a 无法解码")
		}
	}
	if len(raw) < 3 {
		return nil, errors.New("spade_a too short")
	}
	v8 := len(raw) - int(raw[0]^raw[1]^raw[2]) + 47
	v8 = min(v8, len(raw)-1)
	if v8 < 33 {
		return nil, errors.New("spade_a key length invalid")
	}
	work := append([]byte(nil), raw[1:1+v8]...)
	a, b := byte(85), byte(246)
	for i := 0; i < v8; i++ {
		old := work[i]
		prev := b
		if i&1 == 1 {
			prev = a
			a = old
		} else {
			b = old
		}
		pop := bits.OnesCount(uint(i))
		work[i] = byte(-21 - pop + int(prev^old))
	}
	key, err := hex.DecodeString(string(work[1:33]))
	if err != nil {
		return nil, errors.New("视频密钥格式无效")
	}
	return key, nil
}

func padBase64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

// decryptSpadeURL decrypts a media URL encrypted with the model's key_seed.
func decryptSpadeURL(value string, seed []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(padBase64(strings.TrimSpace(value)))
	if err != nil {
		return "", errors.New("spade URL 无法解码")
	}
	if len(raw) < 5 || raw[0] != 0xa8 || raw[2] != 0x01 || raw[3] != 0x00 {
		return "", errors.New("spade URL header format error")
	}
	n := (len(raw) - 4) / 16 * 16
	if n == 0 {
		return "", errors.New("spade URL ciphertext has no complete block")
	}
	h1 := sha512.Sum512(seed)
	h2 := sha512.Sum512(append(h1[:], kdfSalt...))
	block, _ := aes.NewCipher(h2[:16])
	plain := make([]byte, n)
	cipher.NewCBCDecrypter(block, h2[16:32]).CryptBlocks(plain, raw[4:4+n])
	if p := int(plain[len(plain)-1]); p >= 1 && p <= 16 && p <= len(plain) {
		plain = plain[:len(plain)-p]
	}
	return string(bytes.TrimRight(plain, "\x00")), nil
}

// FFmpeg finds ffmpeg, also where Homebrew puts it, which apps opened from
// Finder do not have on their PATH.
func FFmpeg() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
