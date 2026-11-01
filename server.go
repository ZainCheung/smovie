package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var vidPattern = regexp.MustCompile(`^\d{1,30}$`)

// episodeInfo is the episode the player page shows.
type episodeInfo struct {
	VID    string `json:"vid"`
	Ep     int    `json:"ep"`
	Total  int    `json:"total"`
	Title  string `json:"title"`
	Poster string `json:"poster"`
}

// playerEvents is what the player page tells the app.
type playerEvents struct {
	step  func(vid string, delta int, ended bool)
	retry func(vid string)
	meta  func(vid string, width, height int)
	// fullscreen asks for the player to fill the screen, or no longer.
	fullscreen func(on bool)
}

// mediaServer serves the player page and the decrypted episodes to the
// web view on a loopback port: WebKit plays <video> from http with Range
// requests, which it does not ask of custom schemes reliably.
//
// The page loads once and stays: the app switches episodes inside it, so
// that the player keeps full screen, its volume and its speed.
type mediaServer struct {
	base   string
	lib    *library
	events playerEvents

	mu      sync.Mutex
	current episodeInfo
}

func startMediaServer(lib *library, events playerEvents) (*mediaServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &mediaServer{base: "http://" + ln.Addr().String(), lib: lib, events: events}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /player", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		muted := "false"
		if os.Getenv("SMOVIE_MUTED") != "" {
			muted = "true" // for automated runs
		}
		_, _ = strings.NewReplacer("{{MUTED}}", muted).WriteString(w, playerHTML)
	})
	mux.HandleFunc("GET /media/{file}", s.media)
	mux.HandleFunc("GET /current", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cur := s.current
		s.mu.Unlock()
		writeJSON(w, cur)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		vid := r.URL.Query().Get("v")
		if !vidPattern.MatchString(vid) {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, s.lib.status(vid))
	})
	mux.HandleFunc("POST /step", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if vid := q.Get("v"); vidPattern.MatchString(vid) {
			switch q.Get("to") {
			case "prev":
				s.events.step(vid, -1, false)
			case "next":
				s.events.step(vid, 1, false)
			case "ended":
				s.events.step(vid, 1, true)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /retry", func(w http.ResponseWriter, r *http.Request) {
		if vid := r.URL.Query().Get("v"); vidPattern.MatchString(vid) {
			s.events.retry(vid)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /fullscreen", func(w http.ResponseWriter, r *http.Request) {
		s.events.fullscreen(r.URL.Query().Get("on") == "1")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /meta", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		width, _ := strconv.Atoi(q.Get("w"))
		height, _ := strconv.Atoi(q.Get("h"))
		if vid := q.Get("v"); vidPattern.MatchString(vid) && width > 0 && height > 0 {
			s.events.meta(vid, width, height)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	go http.Serve(ln, mux)
	return s, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *mediaServer) playerURL() string { return s.base + "/player" }

// setCurrent records the episode the page should show, which it reads as
// it loads; the app also tells a loaded page at once.
func (s *mediaServer) setCurrent(info episodeInfo) {
	s.mu.Lock()
	s.current = info
	s.mu.Unlock()
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

const playerHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<style>
*{box-sizing:border-box}
html,body{margin:0;height:100%;background:#000;overflow:hidden;color:#fff;
  font-family:-apple-system,"PingFang SC",sans-serif;-webkit-user-select:none;user-select:none;cursor:default}
#stage{position:fixed;inset:0;background:#000}
#stage.idle{cursor:none}
video{position:absolute;inset:0;width:100%;height:100%;object-fit:contain;background:#000;outline:none}
button{font:inherit;color:inherit;background:none;border:0;padding:0;cursor:pointer}
svg{display:block}
.layer{position:absolute;inset:0;pointer-events:none}
#chrome{opacity:0;transition:opacity .25s}
#stage.active #chrome{opacity:1}
#stage.active #chrome *{pointer-events:auto}
.shade-top{position:absolute;left:0;right:0;top:0;height:110px;background:linear-gradient(rgba(0,0,0,.7),transparent)}
.shade-bottom{position:absolute;left:0;right:0;bottom:0;height:150px;background:linear-gradient(transparent,rgba(0,0,0,.85))}
.top{position:absolute;left:20px;right:20px;top:16px;display:flex;align-items:baseline;gap:12px;white-space:nowrap;overflow:hidden}
.top .title{font-size:16px;font-weight:700;overflow:hidden;text-overflow:ellipsis}
.top .ep{font-size:13px;color:#ccc;flex:none}
.side{position:absolute;top:50%;width:52px;height:76px;margin-top:-38px;border-radius:12px;
  background:rgba(0,0,0,.4);-webkit-backdrop-filter:blur(14px);display:flex;flex-direction:column;
  align-items:center;justify-content:center;gap:2px;transition:background .15s,transform .15s}
.side:hover{background:rgba(229,9,20,.92)}
.side:active{transform:scale(.94)}
.side span{font-size:10px;opacity:.9}
.side[hidden]{display:none}
#prevSide{left:14px} #nextSide{right:14px}
.bottom{position:absolute;left:0;right:0;bottom:0;padding:0 18px 12px}
#bar{position:relative;height:18px;display:flex;align-items:center;cursor:pointer}
#bar .track{position:absolute;left:0;right:0;height:4px;border-radius:2px;background:rgba(255,255,255,.25);transition:height .12s}
#bar:hover .track{height:6px}
#buf,#fill{position:absolute;left:0;top:0;bottom:0;border-radius:2px}
#buf{background:rgba(255,255,255,.35)}
#fill{background:#e50914}
#knob{position:absolute;width:14px;height:14px;margin-left:-7px;border-radius:50%;background:#e50914;transform:scale(0);transition:transform .12s}
#bar:hover #knob,#bar.drag #knob{transform:scale(1)}
#hover{position:absolute;bottom:20px;padding:3px 7px;border-radius:5px;background:rgba(0,0,0,.8);font-size:11px;
  transform:translateX(-50%);display:none;font-variant-numeric:tabular-nums}
#bar:hover #hover{display:block}
.row{display:flex;align-items:center;gap:6px;margin-top:4px}
.btn{width:38px;height:38px;border-radius:8px;display:flex;align-items:center;justify-content:center;transition:background .12s,transform .12s}
.btn:hover{background:rgba(255,255,255,.14)}
.btn:active{transform:scale(.92)}
.btn[disabled]{opacity:.3;pointer-events:none}
.btn svg{width:22px;height:22px}
.time{font-size:13px;color:#ddd;margin-left:6px;font-variant-numeric:tabular-nums;white-space:nowrap}
.spacer{flex:1}
#vol{width:0;opacity:0;transition:width .2s,opacity .2s;accent-color:#e50914}
.volume:hover #vol{width:80px;opacity:1}
.volume{display:flex;align-items:center}
#speed{min-width:46px;width:auto;padding:0 8px;font-size:13px;font-weight:700}
.center{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;
  background:rgba(0,0,0,.55);text-align:center;padding:24px}
.center[hidden]{display:none}
.center h2{margin:0;font-size:20px}
.center p{margin:0;font-size:13px;color:#bbb}
.spinner{width:38px;height:38px;border:3px solid rgba(255,255,255,.2);border-top-color:#e50914;border-radius:50%;animation:spin .8s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.progress{width:min(240px,60%);height:4px;border-radius:2px;background:rgba(255,255,255,.2);overflow:hidden}
.progress div{height:100%;width:0;background:#e50914;transition:width .2s}
.pill{display:inline-flex;align-items:center;gap:8px;padding:10px 22px;border-radius:6px;font-size:15px;font-weight:700;
  background:rgba(109,109,110,.7);pointer-events:auto}
.pill.primary{background:#fff;color:#000}
.pill:hover{filter:brightness(1.1)}
.pill svg{width:18px;height:18px}
#flash{position:absolute;top:50%;left:50%;width:72px;height:72px;margin:-36px 0 0 -36px;border-radius:50%;
  background:rgba(0,0,0,.5);display:flex;align-items:center;justify-content:center;opacity:0;transform:scale(.8);pointer-events:none}
#flash.show{animation:flash .5s ease-out}
#flash svg{width:32px;height:32px}
@keyframes flash{0%{opacity:1;transform:scale(.8)}100%{opacity:0;transform:scale(1.3)}}
#toast{position:absolute;left:50%;top:64px;transform:translateX(-50%);padding:7px 14px;border-radius:8px;background:rgba(0,0,0,.75);
  font-size:13px;opacity:0;transition:opacity .25s;pointer-events:none}
#toast.show{opacity:1}
</style></head>
<body>
<div id="stage">
  <video id="v" playsinline preload="auto"></video>
  <div id="flash"></div>
  <div class="layer" id="chrome">
    <div class="shade-top"></div><div class="shade-bottom"></div>
    <div class="top"><div class="title" id="title"></div><div class="ep" id="eplabel"></div></div>
    <button class="side" id="prevSide" title="上一集 (Shift+←)"><svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg><span>上一集</span></button>
    <button class="side" id="nextSide" title="下一集 (Shift+→)"><svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg><span>下一集</span></button>
    <div class="bottom">
      <div id="bar"><div class="track"><div id="buf"></div><div id="fill"></div></div><div id="knob"></div><div id="hover"></div></div>
      <div class="row">
        <button class="btn" id="play" title="播放/暂停 (空格)"></button>
        <button class="btn" id="prev" title="上一集 (Shift+←)"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M7 5h2v14H7zM20 5.5v13a.5.5 0 0 1-.8.4l-9-6.5a.5.5 0 0 1 0-.8l9-6.5a.5.5 0 0 1 .8.4z"/></svg></button>
        <button class="btn" id="next" title="下一集 (Shift+→)"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M15 5h2v14h-2zM4 5.5v13a.5.5 0 0 0 .8.4l9-6.5a.5.5 0 0 0 0-.8l-9-6.5a.5.5 0 0 0-.8.4z"/></svg></button>
        <div class="volume"><button class="btn" id="mute" title="静音 (M)"></button><input id="vol" type="range" min="0" max="1" step="0.02"></div>
        <div class="time" id="time">0:00 / 0:00</div>
        <div class="spacer"></div>
        <button class="btn" id="speed" title="倍速">1x</button>
        <button class="btn" id="pip" title="画中画"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"><rect x="2.5" y="4.5" width="19" height="15" rx="2"/><rect x="12" y="12" width="7" height="5" rx="1" fill="currentColor"/></svg></button>
        <button class="btn" id="fs" title="全屏 (F)"></button>
      </div>
    </div>
  </div>
  <div class="center" id="loading" hidden>
    <div class="spinner"></div><h2 id="ltitle"></h2><p id="lsub"></p><div class="progress" id="lprog"><div></div></div>
  </div>
  <div class="center" id="error" hidden>
    <h2>播放失败</h2><p id="emsg"></p>
    <button class="pill primary" id="retry">重试</button>
  </div>
  <div class="center" id="ended" hidden>
    <h2 id="endtitle">本集已播完</h2>
    <div style="display:flex;gap:12px">
      <button class="pill" id="replay"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/></svg>重播</button>
      <button class="pill primary" id="endnext"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M6 4.2v15.6a1 1 0 0 0 1.5.86l13-7.8a1 1 0 0 0 0-1.72l-13-7.8A1 1 0 0 0 6 4.2z"/></svg>下一集</button>
    </div>
  </div>
  <div id="toast"></div>
</div>
<script>
const $ = id => document.getElementById(id);
const v = $('v'), stage = $('stage'), MUTED = {{MUTED}};
const I = {
  play: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M6 4.2v15.6a1 1 0 0 0 1.5.86l13-7.8a1 1 0 0 0 0-1.72l-13-7.8A1 1 0 0 0 6 4.2z"/></svg>',
  pause: '<svg viewBox="0 0 24 24" fill="currentColor"><rect x="5" y="4" width="5" height="16" rx="1"/><rect x="14" y="4" width="5" height="16" rx="1"/></svg>',
  vol: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 5 6 9H2v6h4l5 4z" fill="currentColor"/><path d="M15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14"/></svg>',
  mute: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 5 6 9H2v6h4l5 4z" fill="currentColor"/><path d="m22 9-6 6M16 9l6 6"/></svg>',
  fs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3H5a2 2 0 0 0-2 2v3M21 8V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3M16 21h3a2 2 0 0 0 2-2v-3"/></svg>',
  unfs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3v3a2 2 0 0 1-2 2H3M21 8h-3a2 2 0 0 1-2-2V3M3 16h3a2 2 0 0 1 2 2v3M16 21v-3a2 2 0 0 1 2-2h3"/></svg>',
};
const store = {
  get(k, d) { try { const x = localStorage.getItem('smovie.' + k); return x === null ? d : JSON.parse(x); } catch (_) { return d; } },
  set(k, x) { try { localStorage.setItem('smovie.' + k, JSON.stringify(x)); } catch (_) {} },
};
let cur = null, token = 0, sent = false;
const post = (path) => fetch(path, {method: 'POST'}).catch(() => {});
const fmt = s => { s = Math.max(0, Math.floor(s || 0)); const m = Math.floor(s / 60), h = Math.floor(m / 60);
  return (h ? h + ':' + String(m % 60).padStart(2, '0') : m) + ':' + String(s % 60).padStart(2, '0'); };
const show = (id, on) => { $(id).hidden = !on; };
function toast(msg) { const t = $('toast'); t.textContent = msg; t.classList.add('show'); clearTimeout(toast.t); toast.t = setTimeout(() => t.classList.remove('show'), 1600); }
function flash(icon) { const f = $('flash'); f.innerHTML = icon; f.classList.remove('show'); void f.offsetWidth; f.classList.add('show'); }

// --- episodes ---
const hasPrev = () => cur && cur.ep > 1;
const hasNext = () => cur && (!cur.total || cur.ep < cur.total);
function refreshNav() {
  $('prevSide').hidden = !hasPrev(); $('nextSide').hidden = !hasNext();
  $('prev').disabled = !hasPrev(); $('next').disabled = !hasNext();
  $('endnext').hidden = !hasNext();
}
function load(info) {
  if (!info || !info.vid) return;
  if (cur && cur.vid === info.vid && v.getAttribute('src')) { cur = info; refreshNav(); return; }
  cur = info; sent = false; const my = ++token;
  v.pause(); v.removeAttribute('src'); v.load();
  $('title').textContent = info.title || '';
  $('eplabel').textContent = '第 ' + info.ep + ' 集' + (info.total ? ' / 共 ' + info.total + ' 集' : '');
  $('ltitle').textContent = '第 ' + info.ep + ' 集';
  $('lsub').textContent = '正在获取片源…';
  $('lprog').hidden = true;
  show('error', false); show('ended', false); show('loading', true);
  refreshNav(); wake();
  poll(my);
}
async function poll(my) {
  if (my !== token) return;
  let st;
  try { st = await (await fetch('/status?v=' + cur.vid)).json(); } catch (_) { st = {phase: 'resolving'}; }
  if (my !== token) return;
  if (st.phase === 'ready') {
    show('loading', false);
    v.src = '/media/' + cur.vid + '.mp4';
    v.play().catch(() => {});
    return;
  }
  if (st.phase === 'error') {
    show('loading', false); $('emsg').textContent = st.error || '未知错误'; show('error', true); return;
  }
  const mb = n => (n / 1048576).toFixed(1);
  $('lsub').textContent = st.phase === 'downloading' ? ('正在缓冲 ' + (st.quality || '') + (st.total ? '  ' + mb(st.received) + ' / ' + mb(st.total) + ' MB' : ''))
    : st.phase === 'decrypting' ? '正在解密…' : '正在获取片源…';
  $('lprog').hidden = !(st.phase === 'downloading' && st.total);
  if (st.total) $('lprog').firstElementChild.style.width = (100 * st.received / st.total) + '%';
  setTimeout(() => poll(my), 250);
}
function step(to) {
  if (!cur || sent) return;
  if (to === 'prev' && !hasPrev()) return toast('已经是第一集');
  if (to !== 'prev' && !hasNext()) return toast('已经是最后一集');
  sent = true; post('/step?v=' + cur.vid + '&to=' + to);
}
window.smovie = { load, pause: () => v.pause() };

// --- playback ---
function syncPlay() { $('play').innerHTML = v.paused ? I.play : I.pause; }
function syncVol() { $('mute').innerHTML = v.muted || v.volume === 0 ? I.mute : I.vol; $('vol').value = v.muted ? 0 : v.volume; }
function syncTime() {
  const d = v.duration || 0, t = v.currentTime || 0, p = d ? t / d : 0;
  $('fill').style.width = (p * 100) + '%'; $('knob').style.left = (p * 100) + '%';
  $('time').textContent = fmt(t) + ' / ' + fmt(d);
  let b = 0; for (let i = 0; i < v.buffered.length; i++) if (v.buffered.start(i) <= t) b = v.buffered.end(i);
  $('buf').style.width = (d ? b / d * 100 : 0) + '%';
}
const toggle = () => { if (!v.getAttribute('src')) return; if (v.paused) { v.play(); flash(I.play); } else { v.pause(); flash(I.pause); } };
v.addEventListener('play', () => { syncPlay(); show('ended', false); wake(); });
v.addEventListener('pause', () => { syncPlay(); stage.classList.add('active'); stage.classList.remove('idle'); });
v.addEventListener('timeupdate', syncTime);
v.addEventListener('progress', syncTime);
v.addEventListener('durationchange', syncTime);
v.addEventListener('volumechange', () => { syncVol(); if (!MUTED) { store.set('volume', v.volume); store.set('muted', v.muted); } });
v.addEventListener('loadedmetadata', () => {
  v.playbackRate = speeds[speedAt];
  if (cur && v.videoWidth) post('/meta?v=' + cur.vid + '&w=' + v.videoWidth + '&h=' + v.videoHeight);
});
v.addEventListener('ended', () => {
  $('endtitle').textContent = '第 ' + cur.ep + ' 集已播完';
  show('ended', true);
  if (hasNext()) post('/step?v=' + cur.vid + '&to=ended');
  else $('endtitle').textContent = '全剧终';
});
v.addEventListener('error', () => { if (v.getAttribute('src')) { $('emsg').textContent = '视频无法解码，请重试'; show('error', true); } });
v.addEventListener('click', toggle);
v.addEventListener('dblclick', () => toggleFS());

// --- controls ---
$('play').onclick = toggle;
$('prev').onclick = $('prevSide').onclick = () => step('prev');
$('next').onclick = $('nextSide').onclick = $('endnext').onclick = () => step('next');
$('replay').onclick = () => { v.currentTime = 0; v.play(); };
$('retry').onclick = () => { show('error', false); if (cur) { post('/retry?v=' + cur.vid); const c = cur; cur = null; load(c); } };
$('mute').onclick = () => { v.muted = !v.muted; if (!v.muted && v.volume === 0) v.volume = .5; };
$('vol').oninput = e => { v.volume = +e.target.value; v.muted = v.volume === 0; };
const speeds = [1, 1.25, 1.5, 2, 0.75];
let speedAt = Math.max(0, speeds.indexOf(store.get('speed', 1)));
const syncSpeed = () => { $('speed').textContent = speeds[speedAt] + 'x'; v.playbackRate = speeds[speedAt]; };
$('speed').onclick = () => { speedAt = (speedAt + 1) % speeds.length; syncSpeed(); store.set('speed', speeds[speedAt]); toast('播放速度 ' + speeds[speedAt] + 'x'); };
$('pip').onclick = () => {
  try { if (v.webkitSetPresentationMode) v.webkitSetPresentationMode(v.webkitPresentationMode === 'picture-in-picture' ? 'inline' : 'picture-in-picture');
        else if (v.requestPictureInPicture) v.requestPictureInPicture(); } catch (_) {}
};
// Full screen is the app's: its window fills the screen with the player
// alone, so that the player's own controls stay, which the element full
// screen of a <video> would leave behind.
let isFS = false;
const toggleFS = () => post('/fullscreen?on=' + (isFS ? 0 : 1));
const syncFS = () => { $('fs').innerHTML = isFS ? I.unfs : I.fs; $('fs').title = isFS ? '退出全屏 (F / Esc)' : '全屏 (F)'; };
window.smovie.setFullscreen = on => { isFS = !!on; syncFS(); wake(); };
$('fs').onclick = toggleFS;

// Seeking along the bar.
const bar = $('bar');
const at = e => { const r = bar.getBoundingClientRect(); return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)); };
bar.addEventListener('pointermove', e => { const p = at(e); $('hover').style.left = (p * 100) + '%'; $('hover').textContent = fmt(p * (v.duration || 0)); });
bar.addEventListener('pointerdown', e => {
  if (!v.duration) return;
  bar.setPointerCapture(e.pointerId); bar.classList.add('drag');
  const seek = ev => { v.currentTime = at(ev) * v.duration; syncTime(); };
  seek(e);
  const move = ev => seek(ev);
  const up = () => { bar.classList.remove('drag'); bar.removeEventListener('pointermove', move); bar.removeEventListener('pointerup', up); };
  bar.addEventListener('pointermove', move); bar.addEventListener('pointerup', up);
});

// The chrome shows while the pointer moves and while paused.
let idleTimer;
function wake() {
  stage.classList.add('active'); stage.classList.remove('idle'); clearTimeout(idleTimer);
  idleTimer = setTimeout(() => { if (!v.paused) { stage.classList.remove('active'); stage.classList.add('idle'); } }, 2500);
}
['mousemove', 'mousedown', 'wheel'].forEach(e => stage.addEventListener(e, wake));
stage.addEventListener('mouseleave', () => { if (!v.paused) stage.classList.remove('active'); });

document.addEventListener('keydown', e => {
  if (e.target.tagName === 'INPUT') return;
  const k = e.key; let handled = true;
  if (k === ' ' || k === 'k') toggle();
  else if (e.shiftKey && k === 'ArrowRight') step('next');
  else if (e.shiftKey && k === 'ArrowLeft') step('prev');
  else if (k === 'ArrowRight') { v.currentTime = Math.min(v.duration || 0, v.currentTime + 5); toast('快进 5 秒'); }
  else if (k === 'ArrowLeft') { v.currentTime = Math.max(0, v.currentTime - 5); toast('快退 5 秒'); }
  else if (k === 'ArrowUp') { v.volume = Math.min(1, v.volume + .1); v.muted = false; toast('音量 ' + Math.round(v.volume * 100) + '%'); }
  else if (k === 'ArrowDown') { v.volume = Math.max(0, v.volume - .1); toast('音量 ' + Math.round(v.volume * 100) + '%'); }
  else if (k === 'f' || k === 'F') toggleFS();
  else if (k === 'Escape' && isFS) toggleFS();
  else if (k === 'm' || k === 'M') v.muted = !v.muted;
  else handled = false;
  if (handled) { e.preventDefault(); wake(); }
});

v.volume = store.get('volume', 1); v.muted = MUTED || store.get('muted', false);
syncPlay(); syncVol(); syncSpeed(); syncFS(); refreshNav();
fetch('/current').then(r => r.json()).then(load).catch(() => {});
</script>
</body></html>`
