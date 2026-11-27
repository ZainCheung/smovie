// Package hongguo reads the public Hongguo short-drama catalog and resolves
// playable episodes. It is a Go port of the relevant parts of
// hongguo-drama/src/native (GPL-3.0): catalog.js, hongguo-search.js,
// hongguo.js, app-source.js and playback-device.js.
package hongguo

import (
	"context"
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
	"time"
)

const (
	SiteOrigin = "https://hongguoduanju.com"
	webUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var httpClient = &http.Client{Timeout: 20 * time.Second, Transport: directTransport()}

// Series is one drama in a list.
type Series struct {
	ID           string
	Title        string
	Cover        string
	Intro        string
	Tags         []string
	EpisodeCount int
	EpisodeLabel string // "全80集", "更新至12集"…
	Heat         string `json:",omitempty"` // "9667万热度", as rankings tell
}

// Category is a catalog section of the official site.
type Category struct {
	Slug  string
	Label string
}

// Categories lists the browsable video sections.
var Categories = []Category{
	{"real-drama", "真人剧"},
	{"comic-drama", "漫剧"},
	{"ai-drama", "AI 剧"},
}

// Page is one page of a list.
type Page struct {
	Items      []Series
	Page       int
	TotalPages int
	// Genres are the category's own, to narrow it to: those the site
	// lists beside a category.
	Genres []Genre `json:",omitempty"`
}

// Genre is a topic a category narrows to, as 家族 within 真人剧.
type Genre struct {
	Slug  string
	Label string
}

var genrePattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// flexInt decodes a JSON number or a numeric string.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

type rawSeries struct {
	SeriesID         string   `json:"series_id"`
	SeriesName       string   `json:"series_name"`
	SeriesTitle      string   `json:"series_title"`
	SeriesCover      string   `json:"series_cover"`
	SeriesIntro      string   `json:"series_intro"`
	Tags             []string `json:"tags"`
	EpisodeCnt       flexInt  `json:"episode_cnt"`
	EpisodeRightText string   `json:"episode_right_text"`
}

func (r rawSeries) series() Series {
	title := r.SeriesName
	if title == "" {
		title = r.SeriesTitle
	}
	label := strings.TrimSpace(r.EpisodeRightText)
	if label == "" && r.EpisodeCnt > 0 {
		label = fmt.Sprintf("全%d集", r.EpisodeCnt)
	}
	tags := r.Tags
	if len(tags) > 4 {
		tags = tags[:4]
	}
	return Series{
		ID: r.SeriesID, Title: strings.TrimSpace(title), Cover: cleanURL(r.SeriesCover),
		Intro: strings.TrimSpace(r.SeriesIntro), Tags: tags,
		EpisodeCount: int(r.EpisodeCnt), EpisodeLabel: label,
	}
}

var (
	seriesIDPattern = regexp.MustCompile(`^\d{6,30}$`)
	vidPattern      = regexp.MustCompile(`^\d{1,30}$`)
)

func get(ctx context.Context, rawURL string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", SiteOrigin+"/")
	client := httpClient
	if req.URL.Hostname() == siteHost {
		client = siteClient
	}
	res, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
			return nil, errors.New("连接超时，请检查网络后重试")
		}
		return nil, errors.New("网络连接失败，请稍后重试")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 16<<20))
}

var routerDataPattern = regexp.MustCompile(`(?:window\.)?_ROUTER_DATA\s*=\s*\{`)

// readRouterData decodes the page's server-rendered JSON without running it.
func readRouterData(html []byte, v any) error {
	loc := routerDataPattern.FindIndex(html)
	if loc == nil {
		return errors.New("页面未返回目录数据")
	}
	return json.NewDecoder(strings.NewReader(string(html[loc[1]-1:]))).Decode(v)
}

// Browse loads a page of a catalog category.
func Browse(ctx context.Context, category string, page int) (*Page, error) {
	return BrowseGenre(ctx, category, "", page)
}

// BrowseGenre loads a page of a category narrowed to one of its genres;
// all of it for none.
func BrowseGenre(ctx context.Context, category, genre string, page int) (*Page, error) {
	if page < 1 {
		page = 1
	}
	u := SiteOrigin + "/category/" + url.PathEscape(category)
	if genre != "" {
		if !genrePattern.MatchString(genre) {
			return nil, errors.New("分类无效")
		}
		u += "/" + genre
	}
	if page > 1 {
		u += "?page=" + strconv.Itoa(page)
	}
	html, err := get(ctx, u, "text/html")
	if err != nil {
		return nil, fmt.Errorf("目录加载失败：%w", err)
	}
	var router struct {
		LoaderData map[string]json.RawMessage `json:"loaderData"`
	}
	if err := readRouterData(html, &router); err != nil {
		return nil, err
	}
	for _, raw := range router.LoaderData {
		var data struct {
			CategoryRoute struct {
				ContentType string `json:"contentType"`
			} `json:"categoryRoute"`
			RecommendList []rawSeries `json:"recommendList"`
			Pagination    struct {
				PageNum    int `json:"pageNum"`
				TotalPages int `json:"totalPages"`
			} `json:"pagination"`
			SelectorList []struct {
				Items []struct {
					ID   string `json:"selector_item_id"`
					Name string `json:"show_name"`
				} `json:"items"`
			} `json:"selectorList"`
		}
		if json.Unmarshal(raw, &data) != nil || data.CategoryRoute.ContentType != category {
			continue
		}
		out := &Page{Page: page, TotalPages: data.Pagination.TotalPages}
		for _, row := range data.SelectorList {
			for _, it := range row.Items {
				if name := strings.TrimSpace(it.Name); genrePattern.MatchString(it.ID) && name != "" {
					out.Genres = append(out.Genres, Genre{Slug: it.ID, Label: name})
				}
			}
		}
		seen := map[string]bool{}
		for _, r := range data.RecommendList {
			s := r.series()
			if !seriesIDPattern.MatchString(s.ID) || s.Title == "" || seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out.Items = append(out.Items, s)
		}
		return out, nil
	}
	return nil, errors.New("来源目录暂不可用")
}

// Search queries the official site's search.
func Search(ctx context.Context, keyword string, page int) (*Page, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, errors.New("请输入搜索词")
	}
	if page < 1 {
		page = 1
	}
	q := url.Values{"aid": {"8662"}, "query": {keyword}, "page": {strconv.Itoa(page)}}
	body, err := get(ctx, SiteOrigin+"/api/search/result?"+q.Encode(), "application/json, text/plain, */*")
	if err != nil {
		return nil, fmt.Errorf("搜索失败：%w", err)
	}
	var payload struct {
		SearchList []struct {
			Keyword   string          `json:"keyword"`
			Name      string          `json:"name"`
			VideoData json.RawMessage `json:"video_data"`
		} `json:"searchList"`
		TotalPages int  `json:"totalPages"`
		HasMore    bool `json:"hasMore"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("搜索返回异常数据")
	}
	out := &Page{Page: page, TotalPages: payload.TotalPages}
	if payload.HasMore && out.TotalPages <= page {
		out.TotalPages = page + 1
	}
	seen := map[string]bool{}
	for _, item := range payload.SearchList {
		var r rawSeries
		_ = json.Unmarshal(item.VideoData, &r)
		if r.SeriesID == "" {
			r.SeriesID = item.Keyword
		}
		if r.SeriesName == "" && r.SeriesTitle == "" {
			r.SeriesName = item.Name
		}
		s := r.series()
		if !seriesIDPattern.MatchString(s.ID) || s.Title == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out.Items = append(out.Items, s)
	}
	return out, nil
}

// Detail is a series with its episodes, in order.
type Detail struct {
	Series
	VIDs []string
	// AlbumID is the collection the series is a season of, when it is.
	AlbumID string
}

const (
	videoDetailPath             = "/novel/player/video_detail/v1/"
	multiVideoDetailPreloadPath = "/novel/player/multi_video_detail/preload/v1"
)

// ErrNotReleased indicates the upstream service recognizes the series ID but
// reports zero episodes (an upcoming or unreleased season/drama).
var ErrNotReleased = errors.New("该剧暂未上线，暂无可播放分集")

// Episodes loads a series' full episode list. The official website truncated
// vid_list to preview episodes and replaced numeric IDs with obfuscated v1_
// tokens, so this prefers signed App detail endpoints (openhongguo first, then
// built-in signed video_detail/v1 and multi_video_detail/preload/v1) and keeps
// the public website as a last-resort fallback.
func Episodes(ctx context.Context, seriesID string) (*Detail, error) {
	if !seriesIDPattern.MatchString(seriesID) {
		return nil, errors.New("剧集编号无效")
	}
	var lastErr error
	if isOpenhongguoBackupEnabled() {
		if d, err := episodesViaOpenhongguo(ctx, seriesID); err == nil {
			return d, nil
		} else {
			if errors.Is(err, ErrNotReleased) {
				return nil, ErrNotReleased
			}
			if ctx.Err() != nil {
				return nil, fmt.Errorf("获取分集失败：%w", ctx.Err())
			}
			lastErr = err
		}
	}
	if d, err := episodesViaAppVideoDetail(ctx, seriesID); err == nil {
		return d, nil
	} else {
		if errors.Is(err, ErrNotReleased) {
			return nil, ErrNotReleased
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("获取分集失败：%w", ctx.Err())
		}
		lastErr = err
	}
	if d, err := episodesViaAppPreload(ctx, seriesID); err == nil {
		return d, nil
	} else {
		if errors.Is(err, ErrNotReleased) {
			return nil, ErrNotReleased
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("获取分集失败：%w", ctx.Err())
		}
		lastErr = err
	}
	for _, u := range []string{
		SiteOrigin + "/player/" + seriesID,
		SiteOrigin + "/detail?series_id=" + seriesID,
	} {
		d, err := episodesFrom(ctx, u, seriesID)
		if err == nil {
			return d, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("获取分集失败：%w", ctx.Err())
		}
		lastErr = err
	}
	return nil, fmt.Errorf("获取分集失败：%w", lastErr)
}

type appSeriesEpisode struct {
	VID      flexString `json:"vid"`
	VideoID  flexString `json:"video_id"`
	ItemID   flexString `json:"item_id"`
	VIDIndex flexInt    `json:"vid_index"`
	Index    flexInt    `json:"index"`
}

func (e appSeriesEpisode) id() string {
	for _, cand := range []flexString{e.VID, e.VideoID, e.ItemID} {
		if s := strings.TrimSpace(string(cand)); s != "" {
			return s
		}
	}
	return ""
}

func (e appSeriesEpisode) order() int {
	if e.VIDIndex != 0 {
		return int(e.VIDIndex)
	}
	return int(e.Index)
}

type appSeriesVideoData struct {
	SeriesIDStr flexString         `json:"series_id_str"`
	SeriesID    flexString         `json:"series_id"`
	Title       string             `json:"series_title"`
	Name        string             `json:"series_name"`
	Intro       string             `json:"series_intro"`
	Cover       string             `json:"series_cover"`
	Episodes    flexInt            `json:"episode_cnt"`
	RightText   string             `json:"episode_right_text"`
	AlbumID     flexString         `json:"related_album_id"`
	VideoList   []appSeriesEpisode `json:"video_list"`
}

func episodesViaAppVideoDetail(ctx context.Context, seriesID string) (*Detail, error) {
	payload, _ := json.Marshal(map[string]any{
		"series_id": seriesID,
		"biz_param": map[string]any{
			"caller_scene":              "single_col",
			"detail_page_version":       0,
			"from_video_id":             "",
			"need_all_video_definition": false,
			"need_mp4_align":            false,
			"source":                    4,
			"video_id_type":             1,
		},
	})
	api := activeProfile().raw.Playback.AppAPIBase + videoDetailPath
	body, err := signedJSON(ctx, api, payload)
	if err != nil {
		return nil, err
	}
	var res struct {
		Code flexString `json:"code"`
		Data struct {
			VideoData appSeriesVideoData `json:"video_data"`
		} `json:"data"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil || (res.Code != "" && res.Code != "0") {
		return nil, errors.New("剧集详情接口响应无效")
	}
	return parseAppSeriesVideoData(seriesID, res.Data.VideoData)
}

func episodesViaAppPreload(ctx context.Context, seriesID string) (*Detail, error) {
	payload, _ := json.Marshal(map[string]any{
		"biz_param": map[string]any{
			"detail_page_version": 0, "disable_digg_stat": false, "need_all_video_definition": false,
			"need_mp4_align": false, "screen_width_px": "900", "source": 7, "use_os_player": false, "use_server_dns": false,
		},
		"dr_scene": "preload", "series_id": seriesID,
	})
	api := activeProfile().raw.Playback.AppAPIBase + multiVideoDetailPreloadPath
	body, err := signedJSON(ctx, api, payload)
	if err != nil {
		return nil, err
	}
	var res struct {
		Code flexString `json:"code"`
		Data map[string]struct {
			VideoData appSeriesVideoData `json:"video_data"`
		} `json:"data"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil || (res.Code != "" && res.Code != "0") {
		return nil, errors.New("剧集预加载接口响应无效")
	}
	entry, ok := res.Data[seriesID]
	if !ok {
		return nil, errors.New("剧集预加载接口未返回该剧集")
	}
	return parseAppSeriesVideoData(seriesID, entry.VideoData)
}

func parseAppSeriesVideoData(seriesID string, v appSeriesVideoData) (*Detail, error) {
	gotID := strings.TrimSpace(string(v.SeriesIDStr))
	if gotID == "" {
		gotID = strings.TrimSpace(string(v.SeriesID))
	}
	if gotID != "" && gotID != seriesID {
		return nil, errors.New("剧集详情接口返回了其他剧集")
	}
	if len(v.VideoList) == 0 {
		if int(v.Episodes) == 0 && (strings.TrimSpace(v.Title) != "" || strings.TrimSpace(v.Name) != "") {
			return nil, ErrNotReleased
		}
		return nil, errors.New("剧集详情接口未返回分集")
	}
	type indexedEp struct {
		id    string
		order int
		pos   int
	}
	items := make([]indexedEp, 0, len(v.VideoList))
	hasOrder := false
	for i, ep := range v.VideoList {
		id := ep.id()
		if !vidPattern.MatchString(id) {
			continue
		}
		ord := ep.order()
		if ord != 0 {
			hasOrder = true
		}
		items = append(items, indexedEp{id: id, order: ord, pos: i})
	}
	if len(items) == 0 {
		return nil, errors.New("分集数据异常")
	}
	if hasOrder {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].order != items[j].order {
				return items[i].order < items[j].order
			}
			return items[i].pos < items[j].pos
		})
	}
	vids := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		if seen[it.id] {
			continue
		}
		seen[it.id] = true
		vids = append(vids, it.id)
	}
	title := strings.TrimSpace(v.Title)
	if title == "" {
		title = strings.TrimSpace(v.Name)
	}
	epCount := int(v.Episodes)
	if epCount < len(vids) {
		epCount = len(vids)
	}
	label := strings.TrimSpace(v.RightText)
	if label == "" && epCount > 0 {
		label = fmt.Sprintf("全%d集", epCount)
	}
	albumID := strings.TrimSpace(string(v.AlbumID))
	if !seriesIDPattern.MatchString(albumID) || albumID == "0" {
		albumID = ""
	}
	return &Detail{
		Series: Series{
			ID:           seriesID,
			Title:        title,
			Cover:        ohgSafeCoverURL(v.Cover),
			Intro:        strings.TrimSpace(v.Intro),
			EpisodeCount: epCount,
			EpisodeLabel: label,
		},
		VIDs:    vids,
		AlbumID: albumID,
	}, nil
}

func episodesFrom(ctx context.Context, u, seriesID string) (*Detail, error) {
	html, err := get(ctx, u, "text/html")
	if err != nil {
		return nil, err
	}
	var router struct {
		LoaderData map[string]json.RawMessage `json:"loaderData"`
	}
	if err := readRouterData(html, &router); err != nil {
		return nil, err
	}
	for _, key := range []string{"player_(series_id)/page", "player_(series_id)/(vid)/page", "detail_page"} {
		raw, ok := router.LoaderData[key]
		if !ok {
			continue
		}
		var data struct {
			SeriesDetail struct {
				rawSeries
				AccessibleEpisodeCnt flexInt           `json:"accessible_episode_cnt"`
				VIDList              []json.RawMessage `json:"vid_list"`
				SeasonInfo           struct {
					GroupID flexString `json:"series_group_id"`
				} `json:"season_info"`
			} `json:"seriesDetail"`
		}
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		sd := data.SeriesDetail
		if sd.SeriesID != seriesID || len(sd.VIDList) == 0 {
			continue
		}
		d := &Detail{Series: sd.series(), AlbumID: string(sd.SeasonInfo.GroupID)}
		if !seriesIDPattern.MatchString(d.AlbumID) {
			d.AlbumID = ""
		}
		for _, v := range sd.VIDList {
			vid := strings.Trim(string(v), `"`)
			if !vidPattern.MatchString(vid) {
				if strings.HasPrefix(vid, "v1_") {
					return nil, errors.New("官网分集编号已混淆")
				}
				return nil, errors.New("分集数据异常")
			}
			d.VIDs = append(d.VIDs, vid)
		}
		if sd.AccessibleEpisodeCnt > 0 && int(sd.EpisodeCnt) > len(d.VIDs) && len(d.VIDs) <= int(sd.AccessibleEpisodeCnt) {
			return nil, errors.New("官网仅返回试看分集")
		}
		if d.EpisodeCount == 0 {
			d.EpisodeCount = len(d.VIDs)
		}
		return d, nil
	}
	return nil, errors.New("页面未找到分集数据")
}

func cleanURL(s string) string {
	r := strings.NewReplacer(`/`, "/", `\/`, "/", `&`, "&", "&amp;", "&")
	return r.Replace(strings.TrimSpace(s))
}

// FetchImage downloads a cover image.
func FetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	return get(ctx, rawURL, "image/webp,image/jpeg,image/png,*/*")
}
