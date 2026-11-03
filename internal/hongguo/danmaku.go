// SPDX-License-Identifier: GPL-3.0-only
// The official danmaku, ported from jackotom/hongguo-drama
// src/native/danmaku.js: the comments the app floats over an episode,
// each at its time. The API is the app's own, signed as it signs it
// (danmaku_sign.go); it may change or refuse at any time, so a failure
// says so and never reads as an episode without danmaku.
package hongguo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Danmaku is one comment floating over an episode at Time seconds.
type Danmaku struct {
	Time  float64 `json:"time"`
	Text  string  `json:"text"`
	Color string  `json:"color"` // #RRGGBB
}

const (
	commentAPI      = "https://api5-normal-sinfonlineb.fqnovel.com/novel/commentapi/comment/list/"
	commentUA       = "com.phoenix.read/73532 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"
	danmakuPage     = 90
	danmakuMaxPages = 40
	danmakuMax      = 3000
	danmakuWindow   = 3600000             // ms of episode a request asks about
	danmakuMaxSpan  = 24 * 60 * 60 * 1000 // the upstream refuses longer windows
	danmakuMaxText  = 200
)

// DanmakuIdentityFile, when set, keeps the anonymous identity the comment
// API sees across launches: it hands each identity its own subset.
var DanmakuIdentityFile string

var (
	danmakuIDMu sync.Mutex
	danmakuID   *[2]string
)

func newDanmakuDeviceID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(8_000_000_000_000_000_000))
	return strconv.FormatUint(1_000_000_000_000_000_000+n.Uint64(), 10)
}

func danmakuIdentity() (deviceID, installID string) {
	danmakuIDMu.Lock()
	defer danmakuIDMu.Unlock()
	if danmakuID != nil {
		return danmakuID[0], danmakuID[1]
	}
	var saved struct {
		DeviceID  string `json:"deviceId"`
		InstallID string `json:"installId"`
	}
	valid := func() bool {
		return len(saved.DeviceID) == 19 && len(saved.InstallID) == 19 && isDigits(saved.DeviceID+saved.InstallID)
	}
	if DanmakuIdentityFile != "" {
		if b, err := os.ReadFile(DanmakuIdentityFile); err == nil {
			_ = json.Unmarshal(b, &saved)
		}
	}
	if !valid() {
		saved.DeviceID, saved.InstallID = newDanmakuDeviceID(), newDanmakuDeviceID()
		if DanmakuIdentityFile != "" {
			b, _ := json.Marshal(saved)
			_ = os.MkdirAll(filepath.Dir(DanmakuIdentityFile), 0o755)
			_ = os.WriteFile(DanmakuIdentityFile, b, 0o600)
		}
	}
	danmakuID = &[2]string{saved.DeviceID, saved.InstallID}
	return saved.DeviceID, saved.InstallID
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// danmakuPageData is a page of the comment list, as far as it is read.
type danmakuPageData struct {
	Code json.RawMessage `json:"code"`
	Data *struct {
		DataList []struct {
			Comment *struct {
				Common struct {
					GroupID flexString `json:"group_id"`
					Status  flexString `json:"status"`
					Content struct {
						Text  string          `json:"text"`
						Color json.RawMessage `json:"color"`
					} `json:"content"`
				} `json:"common"`
				Expand struct {
					OffsetTime json.RawMessage `json:"offset_time"`
				} `json:"expand"`
				OffsetTime json.RawMessage `json:"offset_time"`
			} `json:"comment"`
		} `json:"data_list"`
		Extra struct {
			Next flexString `json:"next_query_danmaku_list_time"`
		} `json:"extra"`
		ListInfo struct {
			HasMore json.RawMessage `json:"has_more"`
			Cursor  json.RawMessage `json:"cursor"`
		} `json:"common_list_info"`
	} `json:"data"`
}

// flexString reads a JSON string or number as its text.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.Trim(string(b), `"`))
	if string(b) == "null" {
		*f = ""
	}
	return nil
}

// parseDanmakuPage returns a page's danmaku of vid, the time to ask from
// next (0 for none) and whether the API told it has more (nil when not).
//
// The next time comes from extra.next_query_danmaku_list_time or from
// common_list_info's cursor, whichever is later: extra is often empty
// while the cursor goes on; an explicit has_more false ends the list.
func parseDanmakuPage(p *danmakuPageData, vid string) (items []Danmaku, next int64, hasMore *bool) {
	if p.Data == nil {
		return nil, 0, nil
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(string(p.Data.Extra.Next)), 10, 64); err == nil {
		next = n
	}
	if raw := bytes.TrimSpace(p.Data.ListInfo.HasMore); len(raw) > 0 && string(raw) != "null" {
		var v bool
		switch t := strings.ToLower(strings.Trim(string(raw), `"`)); t {
		case "true", "1", "yes":
			v = true
		case "false", "0", "no", "":
			v = false
		default:
			v = true
		}
		hasMore = &v
	}
	cursor := p.Data.ListInfo.Cursor
	var s string
	if json.Unmarshal(cursor, &s) == nil {
		cursor = json.RawMessage(s)
	}
	var c map[string]flexString
	if json.Unmarshal(cursor, &c) == nil {
		for _, k := range []string{"end_offset_time", "next_offset_time", "offset"} {
			if n, err := strconv.ParseInt(string(c[k]), 10, 64); err == nil {
				next = max(next, n)
				break
			}
		}
	}
	if hasMore != nil && !*hasMore || next < 0 {
		next = 0
	}
	for _, row := range p.Data.DataList {
		cm := row.Comment
		if cm == nil {
			continue
		}
		// Only this episode's, and only those shown: others are pending or folded.
		if g := string(cm.Common.GroupID); g != "" && g != vid || cm.Common.Status != "1" {
			continue
		}
		at := cm.Expand.OffsetTime
		if len(at) == 0 || string(at) == "null" {
			at = cm.OffsetTime
		}
		ms, err := strconv.ParseFloat(strings.Trim(string(at), `"`), 64)
		if err != nil || ms < 0 || ms > 7*24*3600*1000 {
			continue
		}
		text := cleanDanmaku(cm.Common.Content.Text)
		if text == "" || len([]rune(text)) > danmakuMaxText {
			continue
		}
		items = append(items, Danmaku{Time: ms / 1000, Text: text, Color: danmakuColor(cm.Common.Content.Color)})
	}
	return items, next, hasMore
}

func cleanDanmaku(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s))
}

func danmakuColor(raw json.RawMessage) string {
	const white = "#FFFFFF"
	t := strings.TrimSpace(strings.Trim(string(raw), `"`))
	if t == "" || t == "null" {
		return white
	}
	if t[0] == '#' && len(t) == 7 && isHex(t[1:]) {
		return strings.ToUpper(t)
	}
	n, err := strconv.ParseInt(t, 0, 64)
	if err != nil || n < 0 {
		return white
	}
	return fmt.Sprintf("#%06X", n&0xFFFFFF)
}

func isHex(s string) bool {
	for _, r := range strings.ToLower(s) {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func requestDanmaku(ctx context.Context, seriesID, vid string, start, span int64) (*danmakuPageData, error) {
	now := time.Now()
	deviceID, installID := danmakuIdentity()
	query := commentQuery(now.UnixMilli(), deviceID, installID)
	body, _ := json.Marshal(map[string]any{
		"comment_source": 601, "server_channel": 1000, "group_id": vid, "group_type": 30, "comment_type": 20,
		"sort": 1, "count": danmakuPage, "cursor": "", "aid": 8662, "compliance_status": 0,
		"business_param": map[string]any{"book_id": seriesID, "start_offset_time": start,
			"playlet_item_duration": span, "need_danmaku_guide_type": []int{1, 3, 4, 2}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, commentAPI+vid+"/v1/?"+query, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, commentNonceSize)
	_, _ = rand.Read(nonce)
	for k, v := range map[string]string{
		"User-Agent": commentUA, "Accept": "application/json", "X-XS-From-Web": "0", "Sdk-Version": "2",
		"Content-Type": "application/json; charset=utf-8", "Comment-Source": "601", "Server-Channel": "1000",
	} {
		req.Header.Set(k, v)
	}
	for k, v := range commentHeaders(query, now.Unix(), nonce) {
		req.Header.Set(k, v)
	}
	b, err := doJSON(req)
	if err != nil {
		return nil, err
	}
	var p danmakuPageData
	if json.Unmarshal(b, &p) != nil {
		return nil, errors.New("弹幕接口返回了无效数据")
	}
	if c := strings.Trim(string(p.Code), `"`); c != "0" || p.Data == nil {
		return nil, errors.New("弹幕接口响应无效")
	}
	return &p, nil
}

// FetchDanmaku loads the official danmaku of an episode, by time. With an
// error it may still return those it had before a later page failed.
func FetchDanmaku(ctx context.Context, seriesID, vid string) ([]Danmaku, error) {
	if !seriesIDPattern.MatchString(seriesID) || !vidPattern.MatchString(vid) {
		return nil, errors.New("剧集编号无效")
	}
	var all []Danmaku
	seen := map[string]bool{}
	var start int64
	span := int64(danmakuWindow)
	empty := 0
	var failure error
	for page := 0; page < danmakuMaxPages; page++ {
		if page > 0 {
			select {
			case <-time.After(120 * time.Millisecond): // as gently as the app pages
			case <-ctx.Done():
				return sortDanmaku(all), ctx.Err()
			}
		}
		p, err := requestDanmaku(ctx, seriesID, vid, start, span)
		if err != nil {
			if len(all) > 0 {
				failure = errors.New("后续弹幕加载中断，已保留已获取的部分")
			} else {
				failure = errors.New("弹幕接口暂不可用，请稍后重试")
			}
			break
		}
		items, next, _ := parseDanmakuPage(p, vid)
		if len(items) == 0 {
			empty++
		} else {
			empty = 0
		}
		for _, d := range items {
			k := strconv.FormatFloat(d.Time, 'f', 3, 64) + "|" + d.Text
			if !seen[k] {
				seen[k] = true
				all = append(all, d)
			}
		}
		// The API at times says it has more and gives empty pages: stop.
		if len(all) >= danmakuMax || next == 0 || next <= start || empty >= 2 {
			break
		}
		start = next
		span = min(max(span, start+danmakuWindow), danmakuMaxSpan)
	}
	if len(all) > danmakuMax {
		all = all[:danmakuMax]
	}
	return sortDanmaku(all), failure
}

func sortDanmaku(d []Danmaku) []Danmaku {
	sort.SliceStable(d, func(i, j int) bool { return d[i].Time < d[j].Time })
	return d
}
