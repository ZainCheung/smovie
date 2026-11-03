package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Season is a series as a season of a collection: the official app groups
// a drama's seasons under one album, as its 系列剧 tab shows.
type Season struct {
	Series
	Index int    // 1 for the first season
	Heat  string // "9667万热度"
}

const detailAPI = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_detail/preload/v1"

// seasonOf asks the app's series detail which collection a series is a
// season of, and which season.
func seasonOf(ctx context.Context, seriesID string) (albumID string, s Season, err error) {
	payload, _ := json.Marshal(map[string]any{
		"biz_param": map[string]any{"detail_page_version": 0, "disable_digg_stat": false, "need_all_video_definition": false,
			"need_mp4_align": false, "screen_width_px": "900", "source": 7, "use_os_player": false, "use_server_dns": false},
		"dr_scene": "preload", "series_id": seriesID,
	})
	dev, err := getDevice(ctx, false)
	if err != nil {
		return "", s, err
	}
	req, err := signedRequest(ctx, detailAPI, payload, dev, time.Now())
	if err != nil {
		return "", s, err
	}
	body, err := doJSON(req)
	if err != nil {
		return "", s, err
	}
	var res struct {
		Code flexString `json:"code"`
		Data map[string]struct {
			VideoData struct {
				SeriesID   flexString `json:"series_id_str"`
				Title      string     `json:"series_title"`
				Episodes   flexInt    `json:"episode_cnt"`
				Label      string     `json:"episode_right_text"`
				AlbumID    flexString `json:"related_album_id"`
				Season     flexInt    `json:"season_index"`
				SubTitles  []string   `json:"series_sub_title_list"`
				SeriesCode flexString `json:"series_status"`
			} `json:"video_data"`
		} `json:"data"`
	}
	// Decode numbers as their text: these IDs pass 2^53.
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil || res.Code != "0" {
		return "", s, errors.New("剧集详情接口响应无效")
	}
	d, ok := res.Data[seriesID]
	if !ok || string(d.VideoData.SeriesID) != "" && string(d.VideoData.SeriesID) != seriesID {
		return "", s, errors.New("剧集详情接口返回了其他剧集")
	}
	v := d.VideoData
	s = Season{Series: Series{ID: seriesID, Title: strings.TrimSpace(v.Title), EpisodeCount: int(v.Episodes),
		EpisodeLabel: strings.TrimSpace(v.Label)}, Index: int(v.Season)}
	for _, t := range v.SubTitles {
		if h, ok := strings.CutPrefix(t, "红果热度值"); ok {
			s.Heat = h + "热度"
		}
	}
	if seriesIDPattern.MatchString(string(v.AlbumID)) && string(v.AlbumID) != "0" {
		albumID = string(v.AlbumID)
	}
	return albumID, s, nil
}

// seasonSuffix is what tells a season in a title: 第二季, 第5部, 2.
var seasonSuffix = regexp.MustCompile(`\s*(?:[（(]?第\s*[0-9一二三四五六七八九十百两]+\s*[季部][）)]?|[：:]\s*[^：:]{1,8}篇|\s+\d{1,2})\s*$`)

// seasonBase is a season's title without what tells the season.
func seasonBase(title string) string {
	return strings.TrimSpace(seasonSuffix.ReplaceAllString(strings.TrimSpace(title), ""))
}

func foldTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// Seasons lists the seasons of albumID, the collection s is a season of,
// in order. No API lists a collection, so it searches for the series'
// title without its season and keeps the results the series detail tells
// are seasons of the same collection. Seasons not yet out are not found.
func Seasons(ctx context.Context, s Series, albumID string) ([]Season, error) {
	if !seriesIDPattern.MatchString(albumID) {
		return nil, errors.New("合集编号无效")
	}
	base := seasonBase(s.Title)
	if base == "" {
		base = s.Title
	}
	cands := map[string]Series{s.ID: s}
	order := []string{s.ID}
	key := foldTitle(base)
	for page := 1; page <= 2; page++ {
		p, err := Search(ctx, base, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		for _, it := range p.Items {
			if _, ok := cands[it.ID]; ok || !strings.Contains(foldTitle(it.Title), key) {
				continue
			}
			cands[it.ID] = it
			order = append(order, it.ID)
		}
		if page >= p.TotalPages {
			break
		}
	}
	if len(order) > 24 {
		order = order[:24]
	}
	var (
		mu  sync.Mutex
		out []Season
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)
	)
	var mine error
	for _, id := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			album, se, err := seasonOf(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if id == s.ID && err != nil {
				mine = err
			}
			if err != nil || album != albumID {
				return
			}
			// The site's cover and tags: the app's cover is HEIC.
			c := cands[id]
			c.EpisodeCount = max(c.EpisodeCount, se.EpisodeCount)
			if c.EpisodeLabel == "" {
				c.EpisodeLabel = se.EpisodeLabel
			}
			if se.Title != "" {
				c.Title = se.Title
			}
			se.Series = c
			if se.Index <= 0 {
				se.Index = 1
			}
			out = append(out, se)
		}()
	}
	wg.Wait()
	if len(out) == 0 && mine != nil {
		return nil, mine
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}
