package hongguo

import (
	"os"
	"regexp"
	"testing"
)

// A chart reads from the data the page streams, or, when it leaves it
// out, from its rendered cards.
func TestParseRank(t *testing.T) {
	b, err := os.ReadFile("testdata/rank-hot-drama.html")
	if err != nil {
		t.Fatal(err)
	}
	full := string(b)
	cards := regexp.MustCompile(`<script async data-fn-name="r"[^>]*>`).ReplaceAllString(full, "<script>")
	for name, page := range map[string]string{"data": full, "cards": cards} {
		p, err := parseRank(page, "hot-drama", 1)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(p.Items) != 20 {
			t.Fatalf("%s: %d items", name, len(p.Items))
		}
		first := p.Items[0]
		if first.ID != "7701935931787316286" || first.Title != "十八岁太奶奶驾到，重整家族荣耀第五部" || first.Heat != "9667万热度" || first.Cover == "" {
			t.Errorf("%s: first %+v", name, first)
		}
	}
	if p, _ := parseRank(full, "hot-drama", 1); p.TotalPages != 5 || len(p.Items[0].Tags) == 0 || p.Items[0].EpisodeLabel == "" {
		t.Errorf("data: pages %d, first %+v", p.TotalPages, p.Items[0])
	}
}
