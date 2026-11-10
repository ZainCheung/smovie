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
	"unicode"
)

// Season is a series as a season of a collection: the official app groups
// a drama's seasons under one album, as its 系列剧 tab shows.
type Season struct {
	Series
	Index int    // 1 for the first season
	Heat  string // "9667万热度"
}

const detailAPI = appAPIBase + "/novel/player/multi_video_detail/preload/v1"

// seasonOf asks the app's series detail which collection a series is a
// season of, and which season.
func seasonOf(ctx context.Context, seriesID string) (albumID string, s Season, err error) {
	payload, _ := json.Marshal(map[string]any{
		"biz_param": map[string]any{"detail_page_version": 0, "disable_digg_stat": false, "need_all_video_definition": false,
			"need_mp4_align": false, "screen_width_px": "900", "source": 7, "use_os_player": false, "use_server_dns": false},
		"dr_scene": "preload", "series_id": seriesID,
	})
	body, err := signedJSON(ctx, detailAPI, payload)
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

// seasonFromSite reads which collection a series is a season of from its
// public page, as fast as a page loads and without the app's signature:
// the page tells the collection but not the season, which seasonIndex
// reads from the title.
func seasonFromSite(ctx context.Context, seriesID string) (albumID string, s Season, err error) {
	html, err := get(ctx, SiteOrigin+"/detail?series_id="+seriesID, "text/html")
	if err != nil {
		return "", s, err
	}
	var router struct {
		LoaderData struct {
			Page *struct {
				SeriesDetail struct {
					rawSeries
					SeasonInfo struct {
						GroupID flexString `json:"series_group_id"`
					} `json:"season_info"`
				} `json:"seriesDetail"`
				Social struct {
					Heat struct{ Text string } `json:"hot_score_data"`
				} `json:"seriesSocialInfo"`
			} `json:"detail_page"`
		} `json:"loaderData"`
	}
	if err := readRouterData(html, &router); err != nil {
		return "", s, err
	}
	p := router.LoaderData.Page
	if p == nil || p.SeriesDetail.SeriesID != seriesID {
		return "", s, errors.New("页面未找到剧集信息")
	}
	s = Season{Series: p.SeriesDetail.series(), Index: seasonIndex(p.SeriesDetail.series().Title),
		Heat: strings.TrimSpace(p.Social.Heat.Text)}
	if g := string(p.SeriesDetail.SeasonInfo.GroupID); seriesIDPattern.MatchString(g) {
		albumID = g
	}
	return albumID, s, nil
}

// seasonIndex is the season a title tells, 1 when it tells none.
func seasonIndex(title string) int {
	m := seasonMark.FindStringSubmatch(title)
	if m == nil {
		return 1
	}
	if n, err := strconv.Atoi(m[1]); err == nil {
		return n
	}
	return chineseNumber(m[1])
}

// chineseNumber reads 一 to 九十九 (and 两); 0 for what it cannot.
func chineseNumber(s string) int {
	digit := func(r rune) int {
		for i, d := range []rune("零一二三四五六七八九") {
			if d == r {
				return i
			}
		}
		return -1
	}
	rs := []rune(strings.ReplaceAll(s, "两", "二"))
	switch {
	case len(rs) == 1 && rs[0] == '十':
		return 10
	case len(rs) == 1:
		return max(digit(rs[0]), 0)
	case len(rs) == 2 && rs[0] == '十':
		return 10 + max(digit(rs[1]), 0)
	case len(rs) == 2 && rs[1] == '十':
		return max(digit(rs[0]), 0) * 10
	case len(rs) == 3 && rs[1] == '十':
		return max(digit(rs[0]), 0)*10 + max(digit(rs[2]), 0)
	}
	return 0
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
	return SeasonsProgress(ctx, s, albumID, nil)
}

// SeasonsProgress is Seasons, telling progress the seasons found so far,
// in order, each time it finds more.
func SeasonsProgress(ctx context.Context, s Series, albumID string, progress func([]Season)) ([]Season, error) {
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
		sem := make(chan struct{}, 8)
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
				album, se, err := seasonFromSite(ctx, c.ID)
				if err != nil {
					album, se, err = seasonOf(ctx, c.ID)
				}
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
				if c.Cover == "" {
					c.Cover = se.Cover
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

	// sorted is the seasons found, in order; with found's lock held.
	sorted := func() []Season {
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
		return out
	}
	told := 0
	tell := func() {
		if progress == nil {
			return
		}
		mu.Lock()
		n, list := len(found), sorted()
		mu.Unlock()
		if n > told {
			told = n
			progress(list)
		}
	}

	first, err := search(base, 2)
	if err != nil {
		return nil, err
	}
	// gapQueries are the searches for the seasons missing among those
	// indexes, and the one after the last, each asked once.
	asked := map[int]bool{}
	gapQueries := func(indexes []int, titles []string) []string {
		have, last := map[int]bool{}, 0
		for _, n := range indexes {
			have[n] = true
			last = max(last, n)
		}
		unit, digits := seasonStyle(titles)
		var qs []string
		for n := 1; n <= last+1 && len(qs) < 40; n++ {
			if !have[n] && !asked[n] {
				asked[n] = true
				qs = append(qs, base+"第"+seasonNumeral(n, digits)+unit)
			}
		}
		return qs
	}
	searchAll := func(qs []string) []Series {
		var (
			wg    sync.WaitGroup
			cmu   sync.Mutex
			cands []Series
			sem   = make(chan struct{}, 8)
		)
		for _, q := range qs {
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
		return cands
	}

	// While the first results are checked, already search for the seasons
	// their titles leave out.
	cands := append([]Series{s}, first...)
	var idx []string
	var guess []int
	for _, c := range cands {
		idx = append(idx, c.Title)
		guess = append(guess, seasonIndex(c.Title))
	}
	var more []Series
	var wg0 sync.WaitGroup
	wg0.Add(1)
	go func() { defer wg0.Done(); more = searchAll(gapQueries(guess, idx)) }()
	verify(cands)
	tell()
	wg0.Wait()
	if len(found) == 0 && mine != nil {
		return nil, mine
	}
	verify(more)
	tell()

	// Then those still missing among the seasons found, until a round
	// finds none.
	for round := 0; round < 4 && ctx.Err() == nil; round++ {
		mu.Lock()
		var have []int
		for _, se := range found {
			have = append(have, se.Index)
		}
		qs := gapQueries(have, titles)
		mu.Unlock()
		if len(qs) == 0 {
			break
		}
		before := len(found)
		verify(searchAll(qs))
		tell()
		if len(found) == before {
			break
		}
	}

	// Titles that tell the same season, as two that tell none: ask the
	// app, which knows each one's.
	byIndex := map[int][]string{}
	for id, se := range found {
		byIndex[se.Index] = append(byIndex[se.Index], id)
	}
	var wg sync.WaitGroup
	for _, ids := range byIndex {
		if len(ids) < 2 {
			continue
		}
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, se, err := seasonOf(ctx, id); err == nil && se.Index > 0 {
					mu.Lock()
					f := found[id]
					f.Index = se.Index
					found[id] = f
					mu.Unlock()
				}
			}()
		}
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	return sorted(), nil
}
