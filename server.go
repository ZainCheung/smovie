package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var vidPattern = regexp.MustCompile(`^\d{1,30}$`)

// mediaServer serves the player page and the decrypted episodes to the
// web view on a loopback port: WebKit plays <video> from http with Range
// requests, which it does not ask of custom schemes reliably.
type mediaServer struct {
	base    string
	lib     *library
	onEnded func(vid string)
}

func startMediaServer(lib *library, onEnded func(vid string)) (*mediaServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &mediaServer{base: "http://" + ln.Addr().String(), lib: lib, onEnded: onEnded}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /player", s.player)
	mux.HandleFunc("GET /media/{file}", s.media)
	mux.HandleFunc("POST /ended", func(w http.ResponseWriter, r *http.Request) {
		if vid := r.URL.Query().Get("v"); vidPattern.MatchString(vid) {
			s.onEnded(vid)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	go http.Serve(ln, mux)
	return s, nil
}

func (s *mediaServer) playerURL(vid string) string { return s.base + "/player?v=" + vid }

func (s *mediaServer) media(w http.ResponseWriter, r *http.Request) {
	vid := strings.TrimSuffix(r.PathValue("file"), ".mp4")
	if !vidPattern.MatchString(vid) || !s.lib.cached(vid) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, s.lib.path(vid))
}

func (s *mediaServer) player(w http.ResponseWriter, r *http.Request) {
	vid := r.URL.Query().Get("v")
	if !vidPattern.MatchString(vid) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	muted := ""
	if os.Getenv("SMOVIE_MUTED") != "" {
		muted = " muted" // for automated runs
	}
	fmt.Fprintf(w, playerHTML, vid, muted, vid)
}

// blankHTML shows while an episode is being prepared.
const blankHTML = `<!doctype html><html><body style="margin:0;background:#000"></body></html>`

const playerHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<style>
html,body{margin:0;height:100%%;background:#000;overflow:hidden}
video{width:100%%;height:100%%;object-fit:contain;background:#000;outline:none;display:block}
</style></head>
<body>
<video id="v" src="/media/%s.mp4"%s autoplay controls playsinline preload="auto"></video>
<script>
const v = document.getElementById('v');
v.addEventListener('ended', () => fetch('/ended?v=%s', {method: 'POST'}));
document.addEventListener('keydown', e => {
  if (e.target !== v && e.code === 'Space') { e.preventDefault(); v.paused ? v.play() : v.pause(); }
});
v.focus();
</script>
</body></html>`
