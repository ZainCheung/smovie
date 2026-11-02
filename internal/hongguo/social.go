package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Review is a public comment on a series or an episode.
type Review struct {
	Author  string
	Avatar  string
	Content string
	Time    time.Time
	Likes   int
	Rating  float64 // out of 10, 0 when not rated
}

// Reviews is the public preview of a list of comments: the site shows a
// few, of Total.
type Reviews struct {
	Total int
	Items []Review
}

// Celebrity is a member of a series' cast.
type Celebrity struct {
	Name   string
	Avatar string
	Role   string // "饰 宴矜"
}

// SeriesInfo is what a series' public page tells of it beyond its
// episodes: its standing and the comments on it.
type SeriesInfo struct {
	Rating      float64 // out of 10, 0 when unknown
	RatingCount int
	Likes       int
	Favorites   int
	Heat        string // "3892万热度"
	FirstShown  time.Time
	Tags        []string
	Cast        []Celebrity
	Reviews     Reviews
}

// EpisodeInfo is what an episode's public page tells of it.
type EpisodeInfo struct {
	Likes   int
	Reviews Reviews
}

// ErrLocked tells that the site shows nothing of an episode, past those
// it lets anyone watch.
var ErrLocked = errors.New("官网未开放本集评论")

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseFloat(strings.Trim(string(b), `"`), 64)
	if err != nil {
		n = 0
	}
	*f = flexFloat(n)
	return nil
}

type rawReviews struct {
	TotalCount flexInt `json:"total_count"`
	Reviews    []struct {
		CreateTime flexInt   `json:"create_time"`
		DiggCount  flexInt   `json:"digg_count"`
		Rating     flexFloat `json:"rating"`
		UserName   string    `json:"user_name"`
		UserAvatar string    `json:"user_avatar"`
		Content    string    `json:"content"`
	} `json:"reviews"`
}

func (r rawReviews) reviews() Reviews {
	out := Reviews{Total: int(r.TotalCount)}
	for _, x := range r.Reviews {
		content := emojiCodes.Replace(strings.TrimSpace(x.Content))
		if content == "" {
			continue
		}
		rv := Review{Author: strings.TrimSpace(x.UserName), Avatar: imageURL(x.UserAvatar), Content: content,
			Likes: int(x.DiggCount), Rating: float64(x.Rating)}
		if x.CreateTime > 0 {
			rv.Time = time.Unix(int64(x.CreateTime), 0)
		}
		out.Items = append(out.Items, rv)
	}
	out.Total = max(out.Total, len(out.Items))
	return out
}

// emojiCodes are the app's stickers that comments write as [名字], as the
// emoji closest to them; others stay as written.
var emojiCodes = strings.NewReplacer(
	"[爱慕]", "🥰", "[爽]", "😆", "[舔屏]", "🤤", "[飞吻]", "😘", "[鼓掌]", "👏", "[探究]", "🧐",
	"[奸笑]", "😏", "[捂脸]", "🤦", "[大笑]", "😄", "[微笑]", "🙂", "[流泪]", "😢", "[大哭]", "😭",
	"[哭哭]", "😢", "[泪奔]", "😭", "[害羞]", "😊", "[色]", "😍", "[比心]", "🫶", "[爱心]", "❤️",
	"[心碎]", "💔", "[生气]", "😠", "[发怒]", "😡", "[震惊]", "😱", "[惊讶]", "😮", "[疑问]", "❓",
	"[思考]", "🤔", "[赞]", "👍", "[点赞]", "👍", "[强]", "💪", "[OK]", "👌", "[可怜]", "🥺",
	"[委屈]", "🥺", "[呲牙]", "😁", "[笑哭]", "😂", "[捂嘴笑]", "🤭", "[憨笑]", "😄", "[吃瓜]", "🍉",
	"[玫瑰]", "🌹", "[666]", "6️⃣6️⃣6️⃣", "[酷拽]", "😎", "[白眼]", "🙄", "[尴尬]", "😅", "[晕]", "😵",
	"[再见]", "👋", "[抱抱]", "🤗", "[顶帖]", "🔝", "[星星眼]", "🤩", "[送心]", "💝", "[调皮]", "😜",
)

// imageURL keeps a picture's URL when it is the web's, over https.
func imageURL(s string) string {
	s = cleanURL(s)
	if rest, ok := strings.CutPrefix(s, "http://"); ok {
		s = "https://" + rest
	}
	if !strings.HasPrefix(s, "https://") {
		return ""
	}
	return s
}

// Info loads a series' standing, cast and comments from its public page.
func Info(ctx context.Context, seriesID string) (*SeriesInfo, error) {
	if !seriesIDPattern.MatchString(seriesID) {
		return nil, errors.New("剧集编号无效")
	}
	html, err := get(ctx, SiteOrigin+"/detail?series_id="+seriesID, "text/html")
	if err != nil {
		return nil, err
	}
	var router struct {
		LoaderData struct {
			Page *struct {
				SeriesDetail struct {
					SeriesID    string   `json:"series_id"`
					Tags        []string `json:"tags"`
					FirstShown  flexInt  `json:"first_visible_time"`
					Celebrities []struct {
						Nickname string `json:"nickname"`
						Avatar   string `json:"avatar"`
						SubTitle string `json:"sub_title"`
					} `json:"celebrities"`
				} `json:"seriesDetail"`
				Social struct {
					Rating      flexFloat             `json:"rating"`
					RatingCount flexInt               `json:"rating_count"`
					Likes       flexInt               `json:"series_like_count"`
					Favorites   flexInt               `json:"series_favorite_count"`
					Heat        struct{ Text string } `json:"hot_score_data"`
					Reviews     rawReviews            `json:"reviews"`
				} `json:"seriesSocialInfo"`
			} `json:"detail_page"`
		} `json:"loaderData"`
	}
	if err := readRouterData(html, &router); err != nil {
		return nil, err
	}
	p := router.LoaderData.Page
	if p == nil || p.SeriesDetail.SeriesID != seriesID {
		return nil, errors.New("页面未找到剧集信息")
	}
	s := p.Social
	info := &SeriesInfo{Rating: float64(s.Rating), RatingCount: int(s.RatingCount), Likes: int(s.Likes),
		Favorites: int(s.Favorites), Heat: strings.TrimSpace(s.Heat.Text), Tags: p.SeriesDetail.Tags,
		Reviews: s.Reviews.reviews()}
	if t := int64(p.SeriesDetail.FirstShown); t > 0 {
		info.FirstShown = time.Unix(t, 0)
	}
	for _, c := range p.SeriesDetail.Celebrities {
		if name := strings.TrimSpace(c.Nickname); name != "" {
			info.Cast = append(info.Cast, Celebrity{Name: name, Avatar: imageURL(c.Avatar), Role: strings.TrimSpace(c.SubTitle)})
		}
	}
	return info, nil
}

// Episode loads an episode's likes and comments from its public page. It
// returns ErrLocked for the episodes the site does not show.
func Episode(ctx context.Context, seriesID, vid string) (*EpisodeInfo, error) {
	if !seriesIDPattern.MatchString(seriesID) || !vidPattern.MatchString(vid) {
		return nil, errors.New("剧集编号无效")
	}
	html, err := get(ctx, SiteOrigin+"/player/"+seriesID+"/"+vid, "text/html")
	if err != nil {
		return nil, err
	}
	var router struct {
		LoaderData map[string]json.RawMessage `json:"loaderData"`
	}
	if err := readRouterData(html, &router); err != nil {
		return nil, err
	}
	var data struct {
		SeriesID string `json:"series_id"`
		VID      string `json:"vid"`
		Social   *struct {
			Likes   flexInt    `json:"episode_like_count"`
			Reviews rawReviews `json:"reviews"`
		} `json:"episodeSocialInfo"`
	}
	raw, ok := router.LoaderData["player_(series_id)/(vid)/page"]
	if !ok || json.Unmarshal(raw, &data) != nil || data.SeriesID != seriesID {
		return nil, errors.New("页面未找到分集信息")
	}
	// A locked episode sends to the first one, with pc_locked_episode.
	if data.VID != vid || data.Social == nil {
		return nil, ErrLocked
	}
	return &EpisodeInfo{Likes: int(data.Social.Likes), Reviews: data.Social.Reviews.reviews()}, nil
}
