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
	VID    string  `json:"vid"`
	Ep     int     `json:"ep"`
	Total  int     `json:"total"`
	Title  string  `json:"title"`
	Poster string  `json:"poster"`
	Start  float64 `json:"start,omitempty"` // seconds to pick up at
}

// playerEvents is what the player page tells the app.
type playerEvents struct {
	step  func(vid string, delta int, ended bool)
	retry func(vid string)
	meta  func(vid string, width, height int)
	// fullScreen asks for the player to fill the screen: "on", "off" or
	// "toggle".
	fullScreen func(to string)
	// progress tells where vid is, of its duration, and that it ended.
	progress func(vid string, pos, dur float64, ended bool)
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
		hostFS := strconv.FormatBool(hostFullScreen)
		_, _ = strings.NewReplacer("{{MUTED}}", muted, "{{HOST_FS}}", hostFS).WriteString(w, playerHTML)
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
	mux.HandleFunc("POST /meta", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		width, _ := strconv.Atoi(q.Get("w"))
		height, _ := strconv.Atoi(q.Get("h"))
		if vid := q.Get("v"); vidPattern.MatchString(vid) && width > 0 && height > 0 {
			s.events.meta(vid, width, height)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /fullscreen", func(w http.ResponseWriter, r *http.Request) {
		switch to := r.URL.Query().Get("to"); to {
		case "on", "off", "toggle":
			s.events.fullScreen(to)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /progress", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pos, err1 := strconv.ParseFloat(q.Get("t"), 64)
		dur, err2 := strconv.ParseFloat(q.Get("d"), 64)
		if vid := q.Get("v"); vidPattern.MatchString(vid) && err1 == nil && err2 == nil && pos >= 0 && dur > 0 {
			s.events.progress(vid, min(pos, dur), dur, q.Get("ended") != "")
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

// playerHTML plays episodes with WebKit's own controls. They take no
// buttons of a page's, so previous and next episode float at the sides of
// the picture and answer the media keys through Media Session; and full
// screen, which for a <video> would leave those buttons behind, is the
// page's.
const playerHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<style>
*{box-sizing:border-box}
html,body{margin:0;height:100%;background:#000;overflow:hidden;color:#fff;
  font-family:-apple-system,"PingFang SC",sans-serif;-webkit-user-select:none;user-select:none}
video{position:fixed;inset:0;width:100%;height:100%;object-fit:contain;background:#000;outline:none}
button{font:inherit;color:inherit;border:0;cursor:pointer}
.side{position:fixed;top:50%;width:48px;height:72px;margin-top:-56px;border-radius:12px;
  background:rgba(0,0,0,.42);-webkit-backdrop-filter:blur(14px) saturate(1.4);display:flex;flex-direction:column;
  align-items:center;justify-content:center;gap:2px;opacity:0;transition:opacity .25s,background .15s,transform .15s;pointer-events:none}
body.active .side{opacity:1;pointer-events:auto}
.side:hover{background:rgba(229,9,20,.92)}
.side:active{transform:scale(.94)}
.side span{font-size:10px;opacity:.9}
.side[hidden]{display:none}
#prev{left:12px} #next{right:12px}
.center{position:fixed;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;
  background:rgba(0,0,0,.6);text-align:center;padding:24px}
.center[hidden]{display:none}
.center h2{margin:0;font-size:20px}
.center p{margin:0;font-size:13px;color:#bbb}
.spinner{width:36px;height:36px;border:3px solid rgba(255,255,255,.2);border-top-color:#e50914;border-radius:50%;animation:spin .8s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.progress{width:min(240px,60%);height:4px;border-radius:2px;background:rgba(255,255,255,.2);overflow:hidden}
.progress div{height:100%;width:0;background:#e50914;transition:width .2s}
.pill{display:inline-flex;align-items:center;gap:8px;padding:10px 22px;border-radius:6px;font-size:15px;font-weight:700;background:rgba(109,109,110,.7)}
.pill.primary{background:#fff;color:#000}
.pill[hidden]{display:none}
#hold{position:fixed;left:50%;top:18px;transform:translateX(-50%);display:flex;align-items:center;gap:6px;padding:7px 16px;border-radius:18px;
  background:rgba(0,0,0,.6);-webkit-backdrop-filter:blur(12px);font-size:14px;font-weight:700;opacity:0;transition:opacity .15s;pointer-events:none}
#hold.show{opacity:1}
#hold i{font-style:normal;letter-spacing:-3px;animation:pulse .8s ease-in-out infinite}
@keyframes pulse{50%{opacity:.35}}
#toast{position:fixed;left:50%;top:50%;transform:translate(-50%,-50%);padding:8px 16px;border-radius:10px;background:rgba(0,0,0,.75);
  font-size:13px;opacity:0;transition:opacity .25s;pointer-events:none}
#toast.show{opacity:1}
</style></head>
<body>
<video id="v" controls playsinline preload="auto"></video>
<button class="side" id="prev" title="上一集 (Shift+←)"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg><span>上一集</span></button>
<button class="side" id="next" title="下一集 (Shift+→)"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg><span>下一集</span></button>
<div class="center" id="loading" hidden>
  <div class="spinner"></div><h2 id="ltitle"></h2><p id="lsub"></p><div class="progress" id="lprog"><div></div></div>
</div>
<div class="center" id="error" hidden>
  <h2>播放失败</h2><p id="emsg"></p><button class="pill primary" id="retry">重试</button>
</div>
<div class="center" id="ended" hidden>
  <h2 id="endtitle">本集已播完</h2>
  <div style="display:flex;gap:12px"><button class="pill" id="replay">重播</button><button class="pill primary" id="endnext">下一集</button></div>
</div>
<div id="toast"></div>
<div id="hold">2x 快进中 <i>▶▶</i></div>
<script>
const $ = id => document.getElementById(id);
const v = $('v'), MUTED = {{MUTED}}, HOST_FS = {{HOST_FS}};
let cur = null, token = 0, sent = false;
let lastReport = 0;
// report tells the app where the episode is, for the history.
function report(ended) {
  if (!cur || !v.getAttribute('src') || !isFinite(v.duration) || !v.duration) return;
  lastReport = Date.now();
  post('/progress?v=' + cur.vid + '&t=' + v.currentTime.toFixed(1) + '&d=' + v.duration.toFixed(1) + (ended ? '&ended=1' : ''));
}
const clock = s => { s = Math.floor(s); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
const post = path => fetch(path, {method: 'POST'}).catch(() => {});
const show = (id, on) => { $(id).hidden = !on; };
function toast(msg) { const t = $('toast'); t.textContent = msg; t.classList.add('show'); clearTimeout(toast.t); toast.t = setTimeout(() => t.classList.remove('show'), 1400); }
const store = {
  get(k, d) { try { const x = localStorage.getItem('smovie.' + k); return x === null ? d : JSON.parse(x); } catch (_) { return d; } },
  set(k, x) { try { localStorage.setItem('smovie.' + k, JSON.stringify(x)); } catch (_) {} },
};

// --- episodes ---
const hasPrev = () => cur && cur.ep > 1;
const hasNext = () => cur && (!cur.total || cur.ep < cur.total);
function refreshNav() {
  $('prev').hidden = !hasPrev(); $('next').hidden = !hasNext(); $('endnext').hidden = !hasNext();
  if ('mediaSession' in navigator) {
    try {
      navigator.mediaSession.setActionHandler('previoustrack', hasPrev() ? () => step('prev') : null);
      navigator.mediaSession.setActionHandler('nexttrack', hasNext() ? () => step('next') : null);
    } catch (_) {}
  }
}
function setMetadata(info) {
  if (!('mediaSession' in navigator) || !window.MediaMetadata) return;
  try {
    navigator.mediaSession.metadata = new MediaMetadata({
      title: info.title || '', artist: '第 ' + info.ep + ' 集' + (info.total ? ' / 共 ' + info.total + ' 集' : ''),
      album: '红果短剧', artwork: info.poster ? [{src: info.poster, sizes: '640x853'}] : [],
    });
  } catch (_) {}
}
function load(info) {
  if (!info || !info.vid) return;
  if (cur && cur.vid === info.vid && v.getAttribute('src')) { cur = info; refreshNav(); return; }
  cur = info; sent = false; const my = ++token;
  v.pause(); v.removeAttribute('src'); v.load();
  $('ltitle').textContent = '第 ' + info.ep + ' 集';
  $('lsub').textContent = '正在获取片源…';
  $('lprog').hidden = true;
  show('error', false); show('ended', false); show('loading', true);
  refreshNav(); setMetadata(info);
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
  if (st.phase === 'error') { show('loading', false); $('emsg').textContent = st.error || '未知错误'; show('error', true); return; }
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

// --- full screen: the whole page, not the <video> ---
// WebKit's controls put the <video> alone in full screen, which leaves the
// episode buttons behind, and a page cannot move that full screen to
// itself: entering it took the click's user activation, which another
// request needs. Double-click and F put the page in full screen instead,
// which keeps the controls, the video's own, and the buttons over it; in
// the controls' full screen, Shift+arrows and the media keys change
// episodes.
// With HOST_FS (Windows, Linux) the app fills the screen with the player
// instead: there a page's full screen only fills its web view, which sits
// small in the window, and WebView2 leaves growing it to the app.
const fsEl = () => document.fullscreenElement || document.webkitFullscreenElement;
const root = document.documentElement;
function exitFS() {
  try { if (fsEl()) (document.exitFullscreen || document.webkitExitFullscreen).call(document); } catch (_) {}
}
function toggleFS() {
  if (HOST_FS) return post('/fullscreen?to=toggle');
  try {
    if (fsEl()) (document.exitFullscreen || document.webkitExitFullscreen).call(document);
    else (root.requestFullscreen || root.webkitRequestFullscreen).call(root);
  } catch (_) {}
}
// The <video>'s own full screen button: the app follows it.
function fsChanged() {
  if (HOST_FS) post('/fullscreen?to=' + (fsEl() ? 'on' : 'off'));
  wake();
}
document.addEventListener('fullscreenchange', fsChanged);
document.addEventListener('webkitfullscreenchange', fsChanged);

window.smovie = { load, pause: () => { holdEnd(); v.pause(); }, exitFullScreen: exitFS };

// --- playback ---
v.addEventListener('play', () => { show('ended', false); wake(); });
v.addEventListener('volumechange', () => { if (!MUTED) { store.set('volume', v.volume); store.set('muted', v.muted); } });
v.addEventListener('ratechange', () => { if (v.readyState && !hold.on) store.set('speed', v.playbackRate); });
v.addEventListener('loadedmetadata', () => {
  v.playbackRate = hold.on ? HOLD_RATE : store.get('speed', 1);
  if (cur && cur.start > 3 && cur.start < v.duration - 5) { v.currentTime = cur.start; toast('从 ' + clock(cur.start) + ' 继续播放'); }
  if (cur && v.videoWidth) post('/meta?v=' + cur.vid + '&w=' + v.videoWidth + '&h=' + v.videoHeight);
});
v.addEventListener('timeupdate', () => { if (!v.paused && Date.now() - lastReport > 5000) report(false); });
v.addEventListener('ended', () => {
  report(true);
  $('endtitle').textContent = hasNext() ? '第 ' + cur.ep + ' 集已播完' : '全剧终';
  show('ended', true);
  if (hasNext()) post('/step?v=' + cur.vid + '&to=ended');
});
v.addEventListener('error', () => { if (v.getAttribute('src')) { $('emsg').textContent = '视频无法解码，请重试'; show('error', true); } });
v.addEventListener('dblclick', e => { e.preventDefault(); toggleFS(); });

$('prev').onclick = () => step('prev');
$('next').onclick = $('endnext').onclick = () => step('next');
$('replay').onclick = () => { v.currentTime = 0; v.play(); };
$('retry').onclick = () => { show('error', false); if (cur) { post('/retry?v=' + cur.vid); const c = cur; cur = null; load(c); } };

// The side buttons show with the controls: while the pointer moves, and
// while paused.
let idleTimer;
function wake() {
  document.body.classList.add('active'); clearTimeout(idleTimer);
  idleTimer = setTimeout(() => { if (!v.paused) document.body.classList.remove('active'); }, 2500);
}
['mousemove', 'mousedown'].forEach(e => document.addEventListener(e, wake));
document.addEventListener('mouseleave', () => { if (!v.paused) document.body.classList.remove('active'); });
v.addEventListener('pause', () => { if (!v.ended) report(false); clearTimeout(idleTimer); document.body.classList.add('active'); });

// --- keys, from the page, or from the app while its own UI has the focus ---
const SPEEDS = [0.75, 1, 1.25, 1.5, 2, 3];
function seek(d) {
  if (!v.duration) return;
  v.currentTime = Math.max(0, Math.min(v.duration - 0.1, v.currentTime + d));
  toast((d < 0 ? '快退 ' : '快进 ') + Math.abs(d) + ' 秒');
}
function volume(d) {
  v.muted = false; v.volume = Math.max(0, Math.min(1, Math.round((v.volume + d) * 10) / 10));
  toast('音量 ' + Math.round(v.volume * 100) + '%');
}
function speed(d) {
  let i = SPEEDS.findIndex(x => x >= v.playbackRate - 0.01); if (i < 0) i = 1;
  i = Math.max(0, Math.min(SPEEDS.length - 1, i + d));
  v.playbackRate = SPEEDS[i]; store.set('speed', v.playbackRate);
  toast(v.playbackRate + 'x 倍速');
}
function key(k, shift) {
  if (shift && k === 'ArrowRight') step('next');
  else if (shift && k === 'ArrowLeft') step('prev');
  else if (k === ' ') { if (v.paused) { v.play(); toast('播放'); } else { v.pause(); toast('暂停'); } }
  else if (k === 'ArrowLeft') seek(-5);
  else if (k === 'ArrowRight') seek(5);
  else if (k === 'ArrowUp') volume(0.1);
  else if (k === 'ArrowDown') volume(-0.1);
  else if (k === 'm' || k === 'M') { v.muted = !v.muted; toast(v.muted ? '已静音' : '取消静音'); }
  else if (k === '[' || k === '-') speed(-1);
  else if (k === ']' || k === '=') speed(1);
  else if (k === '\\') { v.playbackRate = 1; store.set('speed', 1); toast('1x 倍速'); }
  else if (k === 'f' || k === 'F') toggleFS();
  else if (k === 'Escape' && HOST_FS) post('/fullscreen?to=off');
  else return false;
  wake(); return true;
}
// Holding → plays at 2x until it is let go, as video apps have it; a
// tap still skips 5 seconds. The OS repeats a held key: a repeat with no
// press seen, as when the app's own UI took the press, starts it too.
const HOLD_RATE = 2, HOLD_DELAY = 300;
const hold = { on: false, timer: 0, rate: 1 };
function holdStart() {
  clearTimeout(hold.timer); hold.timer = 0;
  if (hold.on || !v.getAttribute('src')) return;
  hold.on = true; hold.rate = v.playbackRate;
  v.playbackRate = HOLD_RATE;
  if (v.paused) v.play().catch(() => {});
  $('hold').classList.add('show'); wake();
}
function holdEnd() {
  clearTimeout(hold.timer); hold.timer = 0;
  if (!hold.on) return;
  hold.on = false;
  v.playbackRate = store.get('speed', hold.rate);
  $('hold').classList.remove('show');
}
// In the capture phase, so that the <video>'s own keys don't act twice.
document.addEventListener('keydown', e => {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === 'ArrowRight' && !e.shiftKey) {
    e.preventDefault(); e.stopPropagation();
    if (e.repeat) { if (!hold.on) holdStart(); }
    else if (!hold.timer && !hold.on) hold.timer = setTimeout(holdStart, HOLD_DELAY);
    return;
  }
  if (key(e.key, e.shiftKey)) { e.preventDefault(); e.stopPropagation(); }
}, true);
document.addEventListener('keyup', e => {
  if (e.key !== 'ArrowRight') return;
  e.preventDefault(); e.stopPropagation();
  if (hold.timer) { clearTimeout(hold.timer); hold.timer = 0; seek(5); wake(); }
  else holdEnd();
}, true);
window.addEventListener('blur', holdEnd);
document.addEventListener('visibilitychange', holdEnd);
window.smovie.key = k => k.startsWith('Shift+') ? key(k.slice(6), true) : key(k, false);

v.volume = store.get('volume', 1); v.muted = MUTED || store.get('muted', false);
refreshNav();
fetch('/current').then(r => r.json()).then(load).catch(() => {});
</script>
</body></html>`
