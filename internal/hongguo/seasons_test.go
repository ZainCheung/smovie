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
