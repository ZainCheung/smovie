package hongguo

import "testing"

func TestSeasonBase(t *testing.T) {
	for in, want := range map[string]string{
		"十八岁太奶奶驾到，重整家族荣耀第五部": "十八岁太奶奶驾到，重整家族荣耀",
		"馋坏全皇宫的菜，全靠系统点外卖第二季": "馋坏全皇宫的菜，全靠系统点外卖",
		"开局一碗泡面把陛下吊成翘嘴第一季":   "开局一碗泡面把陛下吊成翘嘴",
		"符仙人：符道之祖第十一季":       "符仙人：符道之祖",
		"归路":                 "归路",
		"闪婚老公是首富 2":          "闪婚老公是首富",
		"天命玄骨（第3季）":          "天命玄骨",
	} {
		if got := seasonBase(in); got != want {
			t.Errorf("seasonBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeasonNumeral(t *testing.T) {
	for n, want := range map[int]string{1: "一", 5: "五", 10: "十", 11: "十一", 19: "十九", 20: "二十", 21: "二十一", 42: "四十二", 99: "九十九", 100: "100"} {
		if got := seasonNumeral(n, false); got != want {
			t.Errorf("seasonNumeral(%d) = %q, want %q", n, got, want)
		}
	}
	if got := seasonNumeral(7, true); got != "7" {
		t.Errorf("digits: %q", got)
	}
	for _, c := range []struct {
		titles []string
		unit   string
		digits bool
	}{
		{[]string{"符仙人：符道之祖", "符仙人：符道之祖第十一季"}, "季", false},
		{[]string{"十八岁太奶奶驾到，重整家族荣耀第五部"}, "部", false},
		{[]string{"凡尘问道（第13季）"}, "季", true},
		{[]string{"归路"}, "季", false},
	} {
		if unit, digits := seasonStyle(c.titles); unit != c.unit || digits != c.digits {
			t.Errorf("seasonStyle(%v) = %s %v", c.titles, unit, digits)
		}
	}
}
