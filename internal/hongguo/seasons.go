package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
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

// seasonNumeral writes n as titles do: in Chinese numerals (第十一季) unless
// digits.
func seasonNumeral(n int, digits bool) string {
	if digits || n < 1 || n > 99 {
		return strconv.Itoa(n)
	}
	const d = "零一二三四五六七八九"
	ch := func(i int) string { return string([]rune(d)[i]) }
	switch {
	case n < 10:
		return ch(n)
	case n < 20:
		if n%10 == 0 {
			return "十"
		}
		return "十" + ch(n%10)
	case n%10 == 0:
		return ch(n/10) + "十"
	}
	return ch(n/10) + "十" + ch(n%10)
}

var seasonMark = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百两]+)\s*([季部])`)

// seasonStyle is how a collection's titles tell their season: 季 or 部, in
// digits or Chinese numerals; 第N季 in Chinese numerals when none tells.
func seasonStyle(titles []string) (unit string, digits bool) {
	for _, t := range titles {
		if m := seasonMark.FindStringSubmatch(t); m != nil {
			_, err := strconv.Atoi(m[1])
			return m[2], err == nil
		}
	}
	return "季", false
}

// Seasons lists the seasons of albumID, the collection s is a season of,
// in order. No API lists a collection, so it searches for the series'
// title without its season and keeps the results the series detail tells
// are seasons of the same collection. The site's search ranks ten a page
// and drifts to other series after the first few seasons, so it then
// searches for each season missing between those found, and the one after
// the last, by its title. Seasons not yet out are not found.
func Seasons(ctx context.Context, s Series, albumID string) ([]Season, error) {
	if !seriesIDPattern.MatchString(albumID) {
		return nil, errors.New("合集编号无效")
	}
	base := seasonBase(s.Title)
	if base == "" {
		base = s.Title
	}
	key := foldTitle(base)
	var (
		mu     sync.Mutex
		found  = map[string]Season{}
		tried  = map[string]bool{}
		titles = []string{s.Title}
		mine   error
	)
	// verify asks the series detail of each new candidate, keeping the
	// seasons of the collection.
	verify := func(cands []Series) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for _, c := range cands {
			mu.Lock()
			skip := tried[c.ID]
			tried[c.ID] = true
			mu.Unlock()
			if skip {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				album, se, err := seasonOf(ctx, c.ID)
				mu.Lock()
				defer mu.Unlock()
				if c.ID == s.ID && err != nil {
					mine = err
				}
				if err != nil || album != albumID {
					return
				}
				// The site's cover and tags: the app's cover is HEIC.
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
				found[c.ID] = se
				titles = append(titles, c.Title)
			}()
		}
		wg.Wait()
	}
	// search returns the results of a query that may be seasons.
	search := func(q string, pages int) ([]Series, error) {
		var out []Series
		for page := 1; page <= pages; page++ {
			p, err := Search(ctx, q, page)
			if err != nil {
				if page == 1 {
					return nil, err
				}
				break
			}
			for _, it := range p.Items {
				if strings.Contains(foldTitle(it.Title), key) {
					out = append(out, it)
				}
			}
			if page >= p.TotalPages {
				break
			}
		}
		return out, nil
	}

	first, err := search(base, 2)
	if err != nil {
		return nil, err
	}
	verify(append([]Series{s}, first...))
	if len(found) == 0 && mine != nil {
		return nil, mine
	}

	// The seasons missing, and the one after the last, by their titles,
	// until a round finds none.
	asked := map[int]bool{}
	for round := 0; round < 4 && ctx.Err() == nil; round++ {
		mu.Lock()
		have, last := map[int]bool{}, 0
		for _, se := range found {
			have[se.Index] = true
			last = max(last, se.Index)
		}
		unit, digits := seasonStyle(titles)
		mu.Unlock()
		var queries []string
		for n := 1; n <= last+1 && len(queries) < 40; n++ {
			if !have[n] && !asked[n] {
				asked[n] = true
				queries = append(queries, base+"第"+seasonNumeral(n, digits)+unit)
			}
		}
		if len(queries) == 0 {
			break
		}
		before := len(found)
		var (
			wg    sync.WaitGroup
			cmu   sync.Mutex
			cands []Series
			sem   = make(chan struct{}, 4)
		)
		for _, q := range queries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if r, err := search(q, 1); err == nil {
					cmu.Lock()
					cands = append(cands, r...)
					cmu.Unlock()
				}
			}()
		}
		wg.Wait()
		verify(cands)
		if len(found) == before {
			break
		}
	}

	out := make([]Season, 0, len(found))
	for _, se := range found {
		out = append(out, se)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
