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
	"strconv"
	"strings"
	"time"
)

const (
	SiteOrigin = "https://hongguoduanju.com"
	webUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

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

// Episodes loads a series' episode list from its public player page.
func Episodes(ctx context.Context, seriesID string) (*Detail, error) {
	if !seriesIDPattern.MatchString(seriesID) {
		return nil, errors.New("剧集编号无效")
	}
	var lastErr error
	for _, u := range []string{
		SiteOrigin + "/player/" + seriesID,
		SiteOrigin + "/detail?series_id=" + seriesID,
	} {
		d, err := episodesFrom(ctx, u, seriesID)
		if err == nil {
			return d, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("获取分集失败：%w", lastErr)
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
				VIDList    []json.RawMessage `json:"vid_list"`
				SeasonInfo struct {
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
				return nil, errors.New("分集数据异常")
			}
			d.VIDs = append(d.VIDs, vid)
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
