package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var vidPattern = regexp.MustCompile(`^\d{1,30}$`)

// mediaServer serves the player page and the decrypted episodes to the
// web view on a loopback port: WebKit plays <video> from http with Range
// requests, which it does not ask of custom schemes reliably.
type mediaServer struct {
	base   string
	lib    *library
	onStep func(vid string, delta int, ended bool)
}

func startMediaServer(lib *library, onStep func(vid string, delta int, ended bool)) (*mediaServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &mediaServer{base: "http://" + ln.Addr().String(), lib: lib, onStep: onStep}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /player", s.player)
	mux.HandleFunc("GET /media/{file}", s.media)
	mux.HandleFunc("POST /step", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		vid := q.Get("v")
		if vidPattern.MatchString(vid) {
			switch q.Get("to") {
			case "prev":
				s.onStep(vid, -1, false)
			case "next":
				s.onStep(vid, 1, false)
			case "ended":
				s.onStep(vid, 1, true)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	go http.Serve(ln, mux)
	return s, nil
}

func (s *mediaServer) playerURL(vid string, episode, total int) string {
	return fmt.Sprintf("%s/player?v=%s&ep=%d&total=%d", s.base, vid, episode, total)
}

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
	q := r.URL.Query()
	vid := q.Get("v")
	if !vidPattern.MatchString(vid) {
		http.NotFound(w, r)
		return
	}
	ep, _ := strconv.Atoi(q.Get("ep"))
	total, _ := strconv.Atoi(q.Get("total"))
	muted := ""
	if os.Getenv("SMOVIE_MUTED") != "" {
		muted = " muted" // for automated runs
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	r2 := strings.NewReplacer("{{VID}}", vid, "{{EP}}", strconv.Itoa(ep), "{{TOTAL}}", strconv.Itoa(total), "{{MUTED}}", muted)
	_, _ = r2.WriteString(w, playerHTML)
}

// blankHTML shows while an episode is being prepared.
const blankHTML = `<!doctype html><html><body style="margin:0;background:#000"></body></html>`

const playerHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<style>
html,body{margin:0;height:100%;background:#000;overflow:hidden;font-family:-apple-system,"PingFang SC",sans-serif;-webkit-user-select:none}
video{width:100%;height:100%;object-fit:contain;background:#000;outline:none;display:block}
.chrome{position:fixed;inset:0;pointer-events:none;opacity:0;transition:opacity .25s}
body.active .chrome{opacity:1}
.label{position:absolute;top:14px;left:50%;transform:translateX(-50%);padding:5px 12px;border-radius:999px;
  background:rgba(0,0,0,.55);-webkit-backdrop-filter:blur(12px);color:#fff;font-size:12px;font-weight:600;letter-spacing:.5px;white-space:nowrap}
.nav{position:absolute;top:50%;transform:translateY(-50%);width:44px;height:64px;border:0;border-radius:12px;
  background:rgba(0,0,0,.45);-webkit-backdrop-filter:blur(12px);color:#fff;cursor:pointer;pointer-events:auto;
  display:flex;align-items:center;justify-content:center;transition:background .15s,transform .15s}
.nav:hover{background:rgba(229,9,20,.9)}
.nav:active{transform:translateY(-50%) scale(.94)}
.nav[disabled]{display:none}
.nav svg{width:22px;height:22px}
#prev{left:10px} #next{right:10px}
.nav .tip{position:absolute;top:100%;margin-top:6px;font-size:11px;white-space:nowrap;opacity:.85}
.toast{position:fixed;bottom:70px;left:50%;transform:translateX(-50%);padding:8px 14px;border-radius:10px;
  background:rgba(0,0,0,.7);color:#fff;font-size:13px;opacity:0;transition:opacity .3s;pointer-events:none}
.toast.show{opacity:1}
</style></head>
<body>
<video id="v" src="/media/{{VID}}.mp4"{{MUTED}} autoplay controls playsinline preload="auto"></video>
<div class="chrome">
  <div class="label">第 {{EP}} 集 / 共 {{TOTAL}} 集</div>
  <button class="nav" id="prev" title="上一集 (Shift+←)" aria-label="上一集">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>
    <span class="tip">上一集</span></button>
  <button class="nav" id="next" title="下一集 (Shift+→)" aria-label="下一集">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>
    <span class="tip">下一集</span></button>
</div>
<div class="toast" id="toast"></div>
<script>
const v = document.getElementById('v'), ep = {{EP}}, total = {{TOTAL}};
const prev = document.getElementById('prev'), next = document.getElementById('next');
prev.disabled = ep <= 1; next.disabled = total > 0 && ep >= total;
let sent = false;
const step = to => { if (sent) return; sent = true; v.pause(); fetch('/step?v={{VID}}&to=' + to, {method: 'POST'}); };
prev.onclick = () => step('prev');
next.onclick = () => step('next');
v.addEventListener('ended', () => {
  if (next.disabled) { show('已经是最后一集'); return; }
  step('ended');
});
let idle;
const wake = () => { document.body.classList.add('active'); clearTimeout(idle);
  idle = setTimeout(() => { if (!v.paused) document.body.classList.remove('active'); }, 2200); };
['mousemove', 'mousedown', 'touchstart'].forEach(e => document.addEventListener(e, wake));
document.addEventListener('mouseleave', () => { if (!v.paused) document.body.classList.remove('active'); });
v.addEventListener('pause', () => document.body.classList.add('active'));
v.addEventListener('play', wake);
const t = document.getElementById('toast');
function show(msg) { t.textContent = msg; t.classList.add('show'); setTimeout(() => t.classList.remove('show'), 1800); }
document.addEventListener('keydown', e => {
  if (e.shiftKey && e.key === 'ArrowRight' && !next.disabled) { e.preventDefault(); step('next'); }
  else if (e.shiftKey && e.key === 'ArrowLeft' && !prev.disabled) { e.preventDefault(); step('prev'); }
  else if (e.target !== v && e.code === 'Space') { e.preventDefault(); v.paused ? v.play() : v.pause(); }
});
wake();
v.focus();
</script>
</body></html>`
