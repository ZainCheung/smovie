package main

import "testing"

func TestSizedURL(t *testing.T) {
	raw := "https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:640:0.image"
	if got, want := sizedURL(raw, imgSmall), "https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:360:0.webp"; got != want {
		t.Errorf("small: %s", got)
	}
	if got, want := sizedURL(raw, imgLarge), "https://p3-novel.byteimg.com/novel-pic/abc~tplv-shrink:720:0.webp"; got != want {
		t.Errorf("large: %s", got)
	}
	other := "https://example.com/a.jpg"
	if sizedURL(other, imgSmall) != other {
		t.Error("an unknown URL changed")
	}
}
