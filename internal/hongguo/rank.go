package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Ranking is one of the site's charts of the series watched most.
type Ranking struct {
	Slug  string
	Label string
}

// Rankings lists the site's charts: all of it, then each category's.
var Rankings = []Ranking{
	{"hot-drama", "热播总榜"},
	{"hot-real-drama", "真人剧榜"},
	{"hot-comic-drama", "漫剧榜"},
	{"hot-ai-drama", "AI 剧榜"},
}

func isRanking(slug string) bool {
	for _, r := range Rankings {
		if r.Slug == slug {
			return true
		}
	}
	return false
}

var (
	scriptTag   = regexp.MustCompile(`<script\b([^>]*)>`)
	fnArgsAttr  = regexp.MustCompile(`data-fn-args=(?:"([^"]*)"|'([^']*)')`)
	rankArticle = regexp.MustCompile(`(?s)<article\b[^>]*>(.*?)</article>`)
	rankHeading = regexp.MustCompile(`(?s)<h2\b[^>]*\bid=["']rank-title-(\d+)["'][^>]*>(.*?)</h2>`)
	rankImage   = regexp.MustCompile(`<img\b[^>]*\bsrc=(?:"([^"]*)"|'([^']*)')`)
	heatText    = regexp.MustCompile(`[\d.,]+\s*[万亿]?\s*热度`)
	anyTag      = regexp.MustCompile(`<[^>]*>`)
)

type rawRankItem struct {
	SeriesID     string   `json:"seriesId"`
	Rank         int      `json:"rank"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	HeatText     string   `json:"heatText"`
	Tags         []string `json:"tags"`
	Description  string   `json:"description"`
	EpisodeCount flexInt  `json:"episodeCount"`
}

// Rank loads a page of a chart, in its order: 20 series a page.
//
// The page streams its data in a script's arguments, which are read as
// JSON and never run; a page that leaves them out still has its cards.
func Rank(ctx context.Context, slug string, page int) (*Page, error) {
	if !isRanking(slug) {
		return nil, errors.New("榜单无效")
	}
	if page < 1 {
		page = 1
	}
	u := SiteOrigin + "/rank/" + slug
	if page > 1 {
		u += "?page=" + strconv.Itoa(page)
	}
	body, err := get(ctx, u, "text/html")
	if err != nil {
		return nil, fmt.Errorf("榜单加载失败：%w", err)
	}
	return parseRank(string(body), slug, page)
}

func parseRank(page string, slug string, n int) (*Page, error) {
	var data struct {
		RankList   []rawRankItem `json:"rankList"`
		Pagination struct {
			PageNum    int `json:"pageNum"`
			TotalPages int `json:"totalPages"`
		} `json:"pagination"`
	}
	route := "rank_" + slug + "/page"
	for _, m := range scriptTag.FindAllStringSubmatch(page, -1) {
		if !strings.Contains(m[1], `data-fn-name="r"`) {
			continue
		}
		a := fnArgsAttr.FindStringSubmatch(m[1])
		if a == nil {
			continue
		}
		var args []json.RawMessage
		if json.Unmarshal([]byte(html.UnescapeString(a[1]+a[2])), &args) != nil || len(args) < 3 {
			continue
		}
		var key, part string
		_ = json.Unmarshal(args[0], &key)
		_ = json.Unmarshal(args[1], &part)
		if key == route && part == "content" && json.Unmarshal(args[2], &data) == nil && len(data.RankList) > 0 {
			break
		}
	}
	if len(data.RankList) == 0 {
		data.RankList = rankCards(page)
	}
	out := &Page{Page: n, TotalPages: data.Pagination.TotalPages}
	if data.Pagination.PageNum > 0 {
		out.Page = data.Pagination.PageNum
	}
	seen := map[string]bool{}
	for _, it := range data.RankList {
		title := strings.TrimSpace(it.Title)
		if !seriesIDPattern.MatchString(it.SeriesID) || title == "" || seen[it.SeriesID] {
			continue
		}
		seen[it.SeriesID] = true
		s := Series{ID: it.SeriesID, Title: title, Cover: cleanURL(it.Cover), Intro: strings.TrimSpace(it.Description),
			Tags: firstTags(it.Tags), EpisodeCount: int(it.EpisodeCount), Heat: strings.TrimSpace(it.HeatText)}
		if s.EpisodeCount > 0 {
			s.EpisodeLabel = fmt.Sprintf("全%d集", s.EpisodeCount)
		}
		if !strings.HasPrefix(s.Cover, "https://") {
			s.Cover = ""
		}
		out.Items = append(out.Items, s)
	}
	if len(out.Items) == 0 {
		return nil, errors.New("榜单暂不可用")
	}
	if out.TotalPages < out.Page {
		out.TotalPages = out.Page
	}
	return out, nil
}

// rankCards reads the chart from its rendered cards, in order.
func rankCards(page string) []rawRankItem {
	var out []rawRankItem
	text := func(s string) string { return strings.TrimSpace(html.UnescapeString(anyTag.ReplaceAllString(s, " "))) }
	for _, m := range rankArticle.FindAllStringSubmatch(page, -1) {
		h := rankHeading.FindStringSubmatch(m[1])
		if h == nil || !strings.Contains(m[1], "/detail?series_id="+h[1]) {
			continue
		}
		it := rawRankItem{SeriesID: h[1], Title: text(h[2]), Rank: len(out) + 1, HeatText: heatText.FindString(text(m[1]))}
		if img := rankImage.FindStringSubmatch(m[1]); img != nil {
			it.Cover = html.UnescapeString(img[1] + img[2])
		}
		out = append(out, it)
	}
	return out
}

func firstTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && len(out) < 4 {
			out = append(out, t)
		}
	}
	return out
}
