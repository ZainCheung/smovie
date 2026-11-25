package hongguo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed profile_default.json
var embeddedProfileJSON []byte

const (
	protocolSchemaVersion = 1
	maxProfileBytes       = 256 << 10 // 256 KiB

	// DefaultProtocolURL points to the signed protocol policy published on
	// GitHub Releases. Clients fetch <url> and <url>.sig, verify Ed25519
	// against the pinned public key, and validate all domain boundaries before
	// activating the policy.
	DefaultProtocolURL = "https://github.com/ZainCheung/smovie/releases/download/protocol-latest/protocol-profile.json"
)

// DefaultProtocolPublicKey is the pinned Ed25519 public key (base64) used to
// verify remote protocol profiles. Maintainers can replace it or override it
// via ProtocolOptions.PublicKey when rotating keys.
// Matches mygo.config.ts updater key by default so a single Ed25519 key pair
// can sign both app releases and protocol hotfix bundles if desired.
const DefaultProtocolPublicKey = "LucWK1ts68/tzDWKR5VcuRyQsAlg9kwPEZnnPT5q9EU="

// Hardcoded top-level domain suffixes that protocol profiles are permitted to
// target. A policy bundle may rotate subdomains or paths within these origins,
// but can never redirect client traffic to arbitrary external hosts (SSRF).
var (
	trustedAPISuffixes       = []string{"fqnovel.com"}
	trustedVideoInfoSuffixes = []string{"snssdk.com"}
	trustedMediaSuffixes     = []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"}
	trustedCatalogSuffixes   = []string{"hongguoduanju.com"}
)

// ProtocolProfile is the declarative, hot-updatable wire policy for Hongguo
// endpoints, headers, route priority, and signing constants.
type ProtocolProfile struct {
	SchemaVersion  int             `json:"schema_version"`
	ProfileVersion string          `json:"profile_version"`
	MinAppVersion  string          `json:"min_app_version,omitempty"`
	MaxAppVersion  string          `json:"max_app_version,omitempty"`
	IssuedAt       string          `json:"issued_at,omitempty"`
	Playback       PlaybackProfile `json:"playback"`
	Catalog        CatalogProfile  `json:"catalog"`
	Danmaku        DanmakuProfile  `json:"danmaku"`
}

// PlaybackProfile configures playback routing, headers, and signing constants.
type PlaybackProfile struct {
	RouteOrder              []string                `json:"route_order,omitempty"`
	AppAPIBase              string                  `json:"app_api_base,omitempty"`
	SingleVideoAPI          string                  `json:"single_video_api,omitempty"`
	MobileVideoAPI          string                  `json:"mobile_video_api,omitempty"`
	TabVideoAPI             string                  `json:"tab_video_api,omitempty"`
	FallbackHost            string                  `json:"fallback_host,omitempty"`
	RegisterURL             string                  `json:"register_url,omitempty"`
	OpenhongguoAPIOrigin    string                  `json:"openhongguo_api_origin,omitempty"`
	OpenhongguoRegOrigin    string                  `json:"openhongguo_register_origin,omitempty"`
	VideoUA                 string                  `json:"video_ua,omitempty"`
	PlaybackUA              string                  `json:"playback_ua,omitempty"`
	RegisterUA              string                  `json:"register_ua,omitempty"`
	MobileUA                string                  `json:"mobile_ua,omitempty"`
	FullMobileUA            string                  `json:"full_mobile_ua,omitempty"`
	DeviceQuery             map[string]string       `json:"device_query,omitempty"`
	FullVersionCode         string                  `json:"full_version_code,omitempty"`
	FullVersionName         string                  `json:"full_version_name,omitempty"`
	MobileVersionCode       string                  `json:"mobile_version_code,omitempty"`
	MobileVersionName       string                  `json:"mobile_version_name,omitempty"`
	MobileChannel           string                  `json:"mobile_channel,omitempty"`
	MobileDeviceType        string                  `json:"mobile_device_type,omitempty"`
	MobileOSVersion         string                  `json:"mobile_os_version,omitempty"`
	MobileDeviceLevel       int                     `json:"mobile_device_level,omitempty"`
	FullDeviceLevel         int                     `json:"full_device_level,omitempty"`
	MedusaV5                MedusaV5Profile         `json:"medusa_v5"`
	Register                RegisterProfile         `json:"register"`
	CodecNegotiation        CodecNegotiationProfile `json:"codec_negotiation"`
	AllowedMediaDomains     []string                `json:"allowed_media_domains,omitempty"`
	AllowedVideoInfoDomains []string                `json:"allowed_video_info_domains,omitempty"`
}

// MedusaV5Profile holds frequently rotated V5 Medusa protocol constants.
type MedusaV5Profile struct {
	License            string `json:"license,omitempty"`
	SDKVersion         string `json:"sdk_version,omitempty"`
	SDKCode            int    `json:"sdk_code,omitempty"`
	EnvVersion         string `json:"env_version,omitempty"`
	FullField21Status  int    `json:"full_field21_status,omitempty"`
	MobileField21State int    `json:"mobile_field21_status,omitempty"`
	FullStateJSON      string `json:"full_state_json,omitempty"`
	MobileStateJSON    string `json:"mobile_state_json,omitempty"`
	MagicHex           string `json:"magic_hex,omitempty"`
	SignKeyHex         string `json:"sign_key_hex,omitempty"`
	SboxHex            string `json:"sbox_hex,omitempty"`
}

// RegisterProfile holds device registration metadata.
type RegisterProfile struct {
	VersionCode int    `json:"version_code,omitempty"`
	AppVersion  string `json:"app_version,omitempty"`
	SDKVersion  string `json:"sdk_version,omitempty"`
	SigHash     string `json:"sig_hash,omitempty"`
	DeviceModel string `json:"device_model,omitempty"`
	DeviceBrand string `json:"device_brand,omitempty"`
}

// CodecNegotiationProfile controls fallback_api HEVC negotiation.
type CodecNegotiationProfile struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	TargetCodecType string `json:"target_codec_type,omitempty"`
}

// CatalogProfile holds web catalog origin settings.
type CatalogProfile struct {
	SiteOrigin string `json:"site_origin,omitempty"`
}

// DanmakuProfile holds comment/danmaku endpoint settings.
type DanmakuProfile struct {
	CommentAPI string `json:"comment_api,omitempty"`
	CommentUA  string `json:"comment_ua,omitempty"`
}

// ProtocolOptions configures where protocol profiles are cached and fetched.
type ProtocolOptions struct {
	CacheDir   string       // directory storing last-good protocol-profile.json(.sig)
	AppVersion string       // running app semver for compatibility range checks
	RemoteURL  string       // defaults to DefaultProtocolURL
	PublicKey  string       // base64 Ed25519 public key; defaults to DefaultProtocolPublicKey
	HTTPClient *http.Client // optional HTTP client for fetching remote profiles
}

// ProtocolStatus reports the currently active protocol policy for the UI.
type ProtocolStatus struct {
	Version   string
	Source    string // "embedded", "cached", "remote"
	UpdatedAt time.Time
	LastError string
	Routes    []string
}

type resolvedProfile struct {
	raw        ProtocolProfile
	source     string
	updatedAt  time.Time
	magic      []byte
	signKey    []byte
	sbox       []byte
	routeSet   map[string]bool
	codecNegOn bool
}

var (
	currentProfile atomic.Pointer[resolvedProfile]
	protocolMu     sync.Mutex
	protocolOpts   ProtocolOptions
	protocolErr    string
)

func init() {
	base, err := compileProfile(embeddedProfileJSON, "", "embedded", time.Time{})
	if err != nil {
		panic("hongguo: invalid embedded protocol profile: " + err.Error())
	}
	currentProfile.Store(base)
}

func activeProfile() *resolvedProfile {
	return currentProfile.Load()
}

// InitProtocol configures protocol hot-update paths and loads a verified
// cached last-good profile from disk when available. Invalid or incompatible
// cached bundles are ignored safely in favor of the embedded baseline.
func InitProtocol(opts ProtocolOptions) {
	protocolMu.Lock()
	defer protocolMu.Unlock()
	if strings.TrimSpace(opts.RemoteURL) == "" {
		opts.RemoteURL = DefaultProtocolURL
	}
	if strings.TrimSpace(opts.PublicKey) == "" {
		opts.PublicKey = DefaultProtocolPublicKey
	}
	protocolOpts = opts
	protocolErr = ""
	if opts.CacheDir == "" {
		return
	}
	pub, err := decodePublicKey(opts.PublicKey)
	if err != nil {
		return
	}
	profilePath := filepath.Join(opts.CacheDir, "protocol-profile.json")
	sigPath := profilePath + ".sig"
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return
	}
	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		return
	}
	if err := verifyProfileSignature(raw, sigBytes, pub); err != nil {
		protocolErr = "本地缓存策略签名无效，已回退内置基线"
		return
	}
	st, _ := os.Stat(profilePath)
	mod := time.Now()
	if st != nil {
		mod = st.ModTime()
	}
	compiled, err := compileProfile(raw, opts.AppVersion, "cached", mod)
	if err != nil {
		protocolErr = "本地缓存策略不可用：" + err.Error()
		return
	}
	currentProfile.Store(compiled)
}

// RefreshProtocol fetches the latest signed protocol policy from the remote
// release URL, verifies its Ed25519 signature, checks version and domain
// constraints, persists it as last-good cache, and activates it atomically.
func RefreshProtocol(ctx context.Context) (ProtocolStatus, error) {
	protocolMu.Lock()
	opts := protocolOpts
	protocolMu.Unlock()

	if strings.TrimSpace(opts.RemoteURL) == "" {
		opts.RemoteURL = DefaultProtocolURL
	}
	if strings.TrimSpace(opts.PublicKey) == "" {
		opts.PublicKey = DefaultProtocolPublicKey
	}
	pub, err := decodePublicKey(opts.PublicKey)
	if err != nil {
		return setProtocolErr(fmt.Errorf("协议公钥配置无效：%w", err))
	}
	if err := validateRemoteProfileURL(opts.RemoteURL); err != nil {
		return setProtocolErr(err)
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	raw, err := fetchBounded(ctx, client, opts.RemoteURL, maxProfileBytes)
	if err != nil {
		return setProtocolErr(fmt.Errorf("拉取协议策略失败：%w", err))
	}
	sigRaw, err := fetchBounded(ctx, client, opts.RemoteURL+".sig", 4096)
	if err != nil {
		return setProtocolErr(fmt.Errorf("拉取策略签名失败：%w", err))
	}
	if err := verifyProfileSignature(raw, sigRaw, pub); err != nil {
		return setProtocolErr(fmt.Errorf("协议策略签名校验失败：%w", err))
	}
	now := time.Now()
	compiled, err := compileProfile(raw, opts.AppVersion, "remote", now)
	if err != nil {
		return setProtocolErr(fmt.Errorf("协议策略校验未通过：%w", err))
	}
	if opts.CacheDir != "" {
		if err := writeProfileCache(opts.CacheDir, raw, sigRaw); err != nil {
			return setProtocolErr(fmt.Errorf("保存策略缓存失败：%w", err))
		}
	}
	currentProfile.Store(compiled)
	resetPlaybackSession()

	protocolMu.Lock()
	protocolErr = ""
	protocolMu.Unlock()
	return CurrentProtocolStatus(), nil
}

// ResetProtocol removes any cached hotfix policy and restores the embedded
// baseline compiled into the binary.
func ResetProtocol() ProtocolStatus {
	protocolMu.Lock()
	cacheDir := protocolOpts.CacheDir
	appVersion := protocolOpts.AppVersion
	protocolErr = ""
	protocolMu.Unlock()

	if cacheDir != "" {
		_ = os.Remove(filepath.Join(cacheDir, "protocol-profile.json"))
		_ = os.Remove(filepath.Join(cacheDir, "protocol-profile.json.sig"))
	}
	base, err := compileProfile(embeddedProfileJSON, appVersion, "embedded", time.Time{})
	if err == nil {
		currentProfile.Store(base)
	}
	resetPlaybackSession()
	return CurrentProtocolStatus()
}

// CurrentProtocolStatus returns the metadata of the currently active policy.
func CurrentProtocolStatus() ProtocolStatus {
	p := activeProfile()
	protocolMu.Lock()
	lastErr := protocolErr
	protocolMu.Unlock()
	routes := append([]string(nil), p.raw.Playback.RouteOrder...)
	return ProtocolStatus{
		Version:   p.raw.ProfileVersion,
		Source:    p.source,
		UpdatedAt: p.updatedAt,
		LastError: lastErr,
		Routes:    routes,
	}
}

func setProtocolErr(err error) (ProtocolStatus, error) {
	protocolMu.Lock()
	protocolErr = err.Error()
	protocolMu.Unlock()
	return CurrentProtocolStatus(), err
}

func resetPlaybackSession() {
	select {
	case playbackGate <- struct{}{}:
		playbackDevice = nil
		<-playbackGate
	default:
	}
	resetOpenhongguoClient()
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("策略文件过大")
	}
	return data, nil
}

func writeProfileCache(dir string, raw, sig []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	profilePath := filepath.Join(dir, "protocol-profile.json")
	sigPath := profilePath + ".sig"
	if err := os.WriteFile(profilePath+".tmp", raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(sigPath+".tmp", sig, 0o644); err != nil {
		_ = os.Remove(profilePath + ".tmp")
		return err
	}
	if err := os.Rename(profilePath+".tmp", profilePath); err != nil {
		return err
	}
	return os.Rename(sigPath+".tmp", sigPath)
}

func decodePublicKey(encoded string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

func verifyProfileSignature(raw, sigRaw []byte, pub ed25519.PublicKey) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("签名格式无效")
	}
	if !ed25519.Verify(pub, raw, sig) {
		return errors.New("签名不匹配")
	}
	return nil
}

func validateRemoteProfileURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("策略地址格式无效")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return errors.New("策略地址必须使用 HTTPS")
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// ValidateProfileJSON parses and validates a profile JSON payload without
// activating it. Used by tests and the offline signing CLI.
func ValidateProfileJSON(raw []byte, appVersion string) (*ProtocolProfile, error) {
	r, err := compileProfile(raw, appVersion, "validated", time.Time{})
	if err != nil {
		return nil, err
	}
	out := r.raw
	return &out, nil
}

func compileProfile(raw []byte, appVersion, source string, updatedAt time.Time) (*resolvedProfile, error) {
	var base ProtocolProfile
	if err := json.Unmarshal(embeddedProfileJSON, &base); err != nil {
		return nil, err
	}
	var p ProtocolProfile
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%w", err)
	}
	if p.SchemaVersion != protocolSchemaVersion {
		return nil, fmt.Errorf("不支持的策略结构版本 %d", p.SchemaVersion)
	}
	if strings.TrimSpace(p.ProfileVersion) == "" {
		return nil, errors.New("缺少 profile_version")
	}
	if appVersion != "" {
		if p.MinAppVersion != "" && compareSemver(appVersion, p.MinAppVersion) < 0 {
			return nil, fmt.Errorf("策略要求客户端版本 >= %s（当前 %s）", p.MinAppVersion, appVersion)
		}
		if p.MaxAppVersion != "" && compareSemver(appVersion, p.MaxAppVersion) > 0 {
			return nil, fmt.Errorf("策略仅适用于客户端版本 <= %s（当前 %s）", p.MaxAppVersion, appVersion)
		}
	}

	mergeProfileDefaults(&p, &base)

	if err := validateProfileURLs(&p); err != nil {
		return nil, err
	}

	r := &resolvedProfile{
		raw:        p,
		source:     source,
		updatedAt:  updatedAt,
		magic:      mobileMagic,
		signKey:    mobileSignKey,
		sbox:       mobileSbox,
		routeSet:   map[string]bool{},
		codecNegOn: true,
	}
	if p.Playback.CodecNegotiation.Enabled != nil {
		r.codecNegOn = *p.Playback.CodecNegotiation.Enabled
	}
	for _, route := range p.Playback.RouteOrder {
		switch route {
		case "video_model_v5", "v5_post", "v5_tab", "v5_full", "openhongguo":
			r.routeSet[route] = true
		default:
			return nil, fmt.Errorf("未知的取源路由 %q", route)
		}
	}
	if len(r.routeSet) == 0 {
		return nil, errors.New("route_order 不能为空")
	}
	if p.Playback.MedusaV5.MagicHex != "" {
		b, err := hex.DecodeString(strings.TrimSpace(p.Playback.MedusaV5.MagicHex))
		if err != nil || len(b) != 16 {
			return nil, errors.New("medusa_v5.magic_hex 必须为 16 字节十六进制")
		}
		r.magic = b
	}
	if p.Playback.MedusaV5.SignKeyHex != "" {
		b, err := hex.DecodeString(strings.TrimSpace(p.Playback.MedusaV5.SignKeyHex))
		if err != nil || len(b) != 32 {
			return nil, errors.New("medusa_v5.sign_key_hex 必须为 32 字节十六进制")
		}
		r.signKey = b
	}
	if p.Playback.MedusaV5.SboxHex != "" {
		b, err := hex.DecodeString(strings.TrimSpace(p.Playback.MedusaV5.SboxHex))
		if err != nil || len(b) != 1024 {
			return nil, errors.New("medusa_v5.sbox_hex 必须为 1024 字节十六进制")
		}
		r.sbox = b
	}
	if p.Playback.MedusaV5.FullStateJSON != "" && !json.Valid([]byte(p.Playback.MedusaV5.FullStateJSON)) {
		return nil, errors.New("medusa_v5.full_state_json 不是合法 JSON")
	}
	if p.Playback.MedusaV5.MobileStateJSON != "" && !json.Valid([]byte(p.Playback.MedusaV5.MobileStateJSON)) {
		return nil, errors.New("medusa_v5.mobile_state_json 不是合法 JSON")
	}
	return r, nil
}

func mergeProfileDefaults(p, base *ProtocolProfile) {
	pb, bpb := &p.Playback, &base.Playback
	if len(pb.RouteOrder) == 0 {
		pb.RouteOrder = append([]string(nil), bpb.RouteOrder...)
	}
	fillString(&pb.AppAPIBase, bpb.AppAPIBase)
	fillString(&pb.SingleVideoAPI, bpb.SingleVideoAPI)
	fillString(&pb.MobileVideoAPI, bpb.MobileVideoAPI)
	fillString(&pb.TabVideoAPI, bpb.TabVideoAPI)
	fillString(&pb.FallbackHost, bpb.FallbackHost)
	fillString(&pb.RegisterURL, bpb.RegisterURL)
	fillString(&pb.VideoUA, bpb.VideoUA)
	fillString(&pb.PlaybackUA, bpb.PlaybackUA)
	fillString(&pb.RegisterUA, bpb.RegisterUA)
	fillString(&pb.MobileUA, bpb.MobileUA)
	fillString(&pb.FullMobileUA, bpb.FullMobileUA)
	fillString(&pb.FullVersionCode, bpb.FullVersionCode)
	fillString(&pb.FullVersionName, bpb.FullVersionName)
	fillString(&pb.MobileVersionCode, bpb.MobileVersionCode)
	fillString(&pb.MobileVersionName, bpb.MobileVersionName)
	fillString(&pb.MobileChannel, bpb.MobileChannel)
	fillString(&pb.MobileDeviceType, bpb.MobileDeviceType)
	fillString(&pb.MobileOSVersion, bpb.MobileOSVersion)
	if pb.MobileDeviceLevel <= 0 {
		pb.MobileDeviceLevel = bpb.MobileDeviceLevel
	}
	if pb.FullDeviceLevel <= 0 {
		pb.FullDeviceLevel = bpb.FullDeviceLevel
	}
	if len(pb.AllowedMediaDomains) == 0 {
		pb.AllowedMediaDomains = append([]string(nil), bpb.AllowedMediaDomains...)
	}
	if len(pb.AllowedVideoInfoDomains) == 0 {
		pb.AllowedVideoInfoDomains = append([]string(nil), bpb.AllowedVideoInfoDomains...)
	}
	fillString(&pb.MedusaV5.License, bpb.MedusaV5.License)
	fillString(&pb.MedusaV5.SDKVersion, bpb.MedusaV5.SDKVersion)
	if pb.MedusaV5.SDKCode == 0 {
		pb.MedusaV5.SDKCode = bpb.MedusaV5.SDKCode
	}
	fillString(&pb.MedusaV5.EnvVersion, bpb.MedusaV5.EnvVersion)
	if pb.MedusaV5.FullField21Status == 0 {
		pb.MedusaV5.FullField21Status = bpb.MedusaV5.FullField21Status
	}
	if pb.MedusaV5.MobileField21State == 0 {
		pb.MedusaV5.MobileField21State = bpb.MedusaV5.MobileField21State
	}
	fillString(&pb.MedusaV5.FullStateJSON, bpb.MedusaV5.FullStateJSON)
	fillString(&pb.MedusaV5.MobileStateJSON, bpb.MedusaV5.MobileStateJSON)

	if pb.Register.VersionCode == 0 {
		pb.Register.VersionCode = bpb.Register.VersionCode
	}
	fillString(&pb.Register.AppVersion, bpb.Register.AppVersion)
	fillString(&pb.Register.SDKVersion, bpb.Register.SDKVersion)
	fillString(&pb.Register.SigHash, bpb.Register.SigHash)
	fillString(&pb.Register.DeviceModel, bpb.Register.DeviceModel)
	fillString(&pb.Register.DeviceBrand, bpb.Register.DeviceBrand)

	fillString(&pb.CodecNegotiation.TargetCodecType, bpb.CodecNegotiation.TargetCodecType)
	fillString(&p.Catalog.SiteOrigin, base.Catalog.SiteOrigin)
	fillString(&p.Danmaku.CommentAPI, base.Danmaku.CommentAPI)
	fillString(&p.Danmaku.CommentUA, base.Danmaku.CommentUA)
}

func fillString(dst *string, fallback string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = fallback
	}
}

func validateProfileURLs(p *ProtocolProfile) error {
	for name, raw := range map[string]string{
		"playback.app_api_base":     p.Playback.AppAPIBase,
		"playback.single_video_api": p.Playback.SingleVideoAPI,
		"playback.mobile_video_api": p.Playback.MobileVideoAPI,
		"playback.tab_video_api":    p.Playback.TabVideoAPI,
		"playback.register_url":     p.Playback.RegisterURL,
		"danmaku.comment_api":       p.Danmaku.CommentAPI,
	} {
		if err := checkTrustedURL(raw, trustedAPISuffixes); err != nil {
			return fmt.Errorf("%s 不受信任：%w", name, err)
		}
	}
	if p.Playback.OpenhongguoAPIOrigin != "" {
		if err := checkTrustedURL(p.Playback.OpenhongguoAPIOrigin, trustedAPISuffixes); err != nil {
			return fmt.Errorf("playback.openhongguo_api_origin 不受信任：%w", err)
		}
	}
	if p.Playback.OpenhongguoRegOrigin != "" {
		if err := checkTrustedURL(p.Playback.OpenhongguoRegOrigin, trustedAPISuffixes); err != nil {
			return fmt.Errorf("playback.openhongguo_register_origin 不受信任：%w", err)
		}
	}
	if !hostMatchesAny(p.Playback.FallbackHost, trustedAPISuffixes) {
		return fmt.Errorf("playback.fallback_host %q 不在允许的域名后缀白名单内", p.Playback.FallbackHost)
	}
	if err := checkTrustedURL(p.Catalog.SiteOrigin, trustedCatalogSuffixes); err != nil {
		return fmt.Errorf("catalog.site_origin 不受信任：%w", err)
	}
	for _, d := range p.Playback.AllowedMediaDomains {
		if !hostMatchesAny(d, trustedMediaSuffixes) {
			return fmt.Errorf("playback.allowed_media_domains 包含未授权域名 %q", d)
		}
	}
	for _, d := range p.Playback.AllowedVideoInfoDomains {
		if !hostMatchesAny(d, trustedVideoInfoSuffixes) {
			return fmt.Errorf("playback.allowed_video_info_domains 包含未授权域名 %q", d)
		}
	}
	return nil
}

func checkTrustedURL(raw string, suffixes []string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Fragment != "" {
		return errors.New("必须是标准 HTTPS 地址")
	}
	if !hostMatchesAny(u.Hostname(), suffixes) {
		return fmt.Errorf("域名 %q 不在允许的白名单内", u.Hostname())
	}
	return nil
}

func hostMatchesAny(host string, suffixes []string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || strings.ContainsAny(h, "/:@?# ") {
		return false
	}
	for _, s := range suffixes {
		if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}

func compareSemver(a, b string) int {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		out[i], _ = strconv.Atoi(parts[i])
	}
	return out
}

func (r *resolvedProfile) hasRoute(name string) bool {
	return r != nil && r.routeSet[name]
}

func (r *resolvedProfile) baseDeviceQuery() map[string]string {
	out := make(map[string]string, len(deviceQuery)+len(r.raw.Playback.DeviceQuery))
	for k, v := range deviceQuery {
		out[k] = v
	}
	for k, v := range r.raw.Playback.DeviceQuery {
		out[k] = v
	}
	return out
}
