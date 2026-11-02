package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
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
		_, _ = strings.NewReplacer("{{MUTED}}", muted,
			"{{HOST_FS}}", strconv.FormatBool(hostFullScreen),
			"{{CUSTOM}}", strconv.FormatBool(customControls())).WriteString(w, playerHTML)
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

// customControls is whether the player draws its own controls: everywhere
// but macOS, whose WebKit controls fit the system, where WebView2's look
// out of place. SMOVIE_CUSTOM_CONTROLS=1 shows them on macOS too.
func customControls() bool {
	return runtime.GOOS != "darwin" || os.Getenv("SMOVIE_CUSTOM_CONTROLS") != ""
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
  font-family:-apple-system,"PingFang SC","Segoe UI","Microsoft YaHei",sans-serif;-webkit-user-select:none;user-select:none}
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
/* --- the custom controls (CUSTOM), in the manner of macOS's --- */
#bar{display:none}
body.custom #bar{display:grid}
body.custom .side{display:none}
body.custom:not(.active){cursor:none}
#bar{position:fixed;left:14px;right:14px;bottom:12px;grid-template-columns:auto 1fr auto;grid-template-areas:"l s r";
  align-items:center;column-gap:10px;padding:6px 10px;border-radius:24px;background:rgba(36,36,38,.62);
  -webkit-backdrop-filter:blur(24px) saturate(1.8);backdrop-filter:blur(24px) saturate(1.8);
  box-shadow:0 8px 30px rgba(0,0,0,.35),inset 0 0 0 .5px rgba(255,255,255,.12);
  opacity:0;transition:opacity .25s;pointer-events:none}
body.active #bar{opacity:1;pointer-events:auto}
.grp{display:flex;align-items:center;gap:2px}
#bl{grid-area:l} #br{grid-area:r} #seek{grid-area:s;display:flex;align-items:center;gap:12px;min-width:0}
.cb{position:relative;width:36px;height:36px;border-radius:18px;background:none;display:flex;align-items:center;justify-content:center;
  color:rgba(255,255,255,.92);transition:background .15s,transform .1s;flex:none}
.cb:hover{background:rgba(255,255,255,.14);color:#fff}
.cb:active{transform:scale(.9)}
.cb[disabled]{opacity:.32;pointer-events:none}
.cb svg{width:22px;height:22px}
svg{display:block}
.cb.big svg{width:26px;height:26px}
.cb .n{position:absolute;font-size:8.5px;font-weight:700;top:50%;left:50%;transform:translate(-50%,-38%)}
.time{font-size:13px;font-weight:600;font-variant-numeric:tabular-nums;color:rgba(255,255,255,.92);flex:none}
.track{position:relative;flex:1;min-width:40px;height:22px;cursor:pointer;touch-action:none}
.track .rail,.track .buf,.track .fill{position:absolute;left:0;top:50%;height:6px;margin-top:-3px;border-radius:3px}
.track .rail{right:0;background:rgba(255,255,255,.22)}
.track .buf{background:rgba(255,255,255,.3);width:0}
.track .fill{background:#fff;width:0}
.track .knob{position:absolute;top:50%;width:16px;height:16px;margin:-8px 0 0 -8px;border-radius:50%;background:#fff;
  box-shadow:0 1px 4px rgba(0,0,0,.4);transition:transform .12s;left:0}
.track:hover .knob,.track.drag .knob{transform:scale(1.15)}
.track:hover .rail,.track:hover .buf,.track:hover .fill,.track.drag .rail,.track.drag .buf,.track.drag .fill{height:8px;margin-top:-4px;border-radius:4px}
.tip{position:absolute;bottom:26px;padding:3px 8px;border-radius:6px;background:rgba(0,0,0,.78);font-size:12px;font-weight:600;
  font-variant-numeric:tabular-nums;transform:translateX(-50%);opacity:0;pointer-events:none;transition:opacity .12s;white-space:nowrap}
.track:hover .tip,.track.drag .tip{opacity:1}
#vol{display:flex;align-items:center}
#vol .track{flex:none;width:0;min-width:0;overflow:hidden;transition:width .2s,margin .2s;margin:0}
#vol:hover .track,#vol .track.drag{width:72px;margin:0 8px 0 2px;overflow:visible}
#vol .knob{width:12px;height:12px;margin:-6px 0 0 -6px}
#spd{width:auto;min-width:44px;padding:0 8px;font-size:13px;font-weight:700;font-variant-numeric:tabular-nums}
.menu{position:absolute;bottom:46px;right:0;display:none;flex-direction:column;padding:6px;border-radius:14px;min-width:112px;
  background:rgba(36,36,38,.86);-webkit-backdrop-filter:blur(24px);backdrop-filter:blur(24px);
  box-shadow:0 10px 30px rgba(0,0,0,.45),inset 0 0 0 .5px rgba(255,255,255,.12)}
.menu.open{display:flex}
.menu button{background:none;text-align:left;padding:7px 12px 7px 28px;border-radius:8px;font-size:13px;font-weight:600;position:relative}
.menu button:hover{background:rgba(255,255,255,.14)}
.menu button.on::before{content:"";position:absolute;left:10px;top:50%;width:6px;height:6px;margin-top:-3px;border-radius:50%;background:#e50914}
.wrap{position:relative}
.fs-exit,body.full .fs-enter{display:none} body.full .fs-exit{display:block}
.pl-pause,body.playing .pl-play{display:none} body.playing .pl-pause{display:block}
.vo-off,body.muted .vo-on{display:none} body.muted .vo-off{display:block}
/* A narrow player, as a portrait episode's: the progress bar takes a row of its own. */
@media (max-width:600px){
  #bar{grid-template-columns:1fr auto;grid-template-areas:"s s" "l r";row-gap:2px;padding:8px 8px 4px;border-radius:18px;left:8px;right:8px;bottom:8px}
  #seek{padding:0 6px}
  .opt{display:none}
}
@media (max-width:380px){ #pip{display:none} }
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
<div id="bar">
  <div class="grp" id="bl">
    <button class="cb" id="cprev" title="上一集 (Shift+←)"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M6 5h2v14H6zM19 5.5v13a1 1 0 0 1-1.5.86l-9.2-6.5a1 1 0 0 1 0-1.72l9.2-6.5A1 1 0 0 1 19 5.5z"/></svg></button>
    <button class="cb opt" id="back" title="快退 10 秒 (←)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12a8 8 0 1 0 2.6-5.9"/><path d="M4 3v5h5"/></svg><span class="n">10</span></button>
    <button class="cb big" id="play" title="播放/暂停 (空格)"><svg viewBox="0 0 24 24" fill="currentColor" class="pl-play"><path d="M7 4.8v14.4a1 1 0 0 0 1.5.86l12-7.2a1 1 0 0 0 0-1.72l-12-7.2A1 1 0 0 0 7 4.8z"/></svg><svg viewBox="0 0 24 24" fill="currentColor" class="pl-pause"><rect x="6" y="4.5" width="4" height="15" rx="1.2"/><rect x="14" y="4.5" width="4" height="15" rx="1.2"/></svg></button>
    <button class="cb opt" id="fwd" title="快进 10 秒 (→)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 12a8 8 0 1 1-2.6-5.9"/><path d="M20 3v5h-5"/></svg><span class="n">10</span></button>
    <button class="cb" id="cnext" title="下一集 (Shift+→)"><svg viewBox="0 0 24 24" fill="currentColor"><path d="M16 5h2v14h-2zM5 5.5v13a1 1 0 0 0 1.5.86l9.2-6.5a1 1 0 0 0 0-1.72l-9.2-6.5A1 1 0 0 0 5 5.5z"/></svg></button>
  </div>
  <div id="seek"><span class="time" id="tcur">00:00</span>
    <div class="track" id="prog"><div class="rail"></div><div class="buf"></div><div class="fill"></div><div class="knob"></div><div class="tip"></div></div>
    <span class="time" id="tdur">00:00</span></div>
  <div class="grp" id="br">
    <div id="vol"><button class="cb" id="mute" title="静音 (M)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="vo-on"><path d="M11 5 6 9H3v6h3l5 4z" fill="currentColor"/><path d="M15.5 8.5a5 5 0 0 1 0 7M18.5 5.5a9 9 0 0 1 0 13"/></svg><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="vo-off"><path d="M11 5 6 9H3v6h3l5 4z" fill="currentColor"/><path d="m16 9 6 6M22 9l-6 6"/></svg></button>
      <div class="track" id="volbar"><div class="rail"></div><div class="fill"></div><div class="knob"></div></div></div>
    <div class="wrap"><button class="cb" id="spd" title="倍速 ([ / ])">1x</button><div class="menu" id="spdmenu"></div></div>
    <button class="cb opt" id="pip" title="画中画"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="4.5" width="19" height="15" rx="2.5"/><rect x="12" y="11.5" width="7" height="5.5" rx="1" fill="currentColor" stroke="none"/></svg></button>
    <button class="cb" id="fs" title="全屏 (F)"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="fs-enter"><path d="M4 9V5a1 1 0 0 1 1-1h4M15 4h4a1 1 0 0 1 1 1v4M20 15v4a1 1 0 0 1-1 1h-4M9 20H5a1 1 0 0 1-1-1v-4"/></svg><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="fs-exit"><path d="M9 4v4a1 1 0 0 1-1 1H4M20 9h-4a1 1 0 0 1-1-1V4M15 20v-4a1 1 0 0 1 1-1h4M4 15h4a1 1 0 0 1 1 1v4"/></svg></button>
  </div>
</div>
<div id="hold">2x 快进中 <i>▶▶</i></div>
<script>
const $ = id => document.getElementById(id);
const v = $('v'), MUTED = {{MUTED}}, HOST_FS = {{HOST_FS}}, CUSTOM = {{CUSTOM}};
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
let refreshNav = function () {
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
  else document.body.classList.toggle('full', !!fsEl());
  wake();
}
document.addEventListener('fullscreenchange', fsChanged);
document.addEventListener('webkitfullscreenchange', fsChanged);

window.smovie = { load, pause: () => { holdEnd(); v.pause(); }, exitFullScreen: exitFS,
  fullScreen: on => document.body.classList.toggle('full', !!on) };

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
  idleTimer = setTimeout(() => { if (!v.paused && !busy()) document.body.classList.remove('active'); }, 2500);
}
// The controls stay while the pointer is on them, drags or a menu is open.
let overBar = false;
const busy = () => overBar || document.querySelector('.track.drag') || $('spdmenu').classList.contains('open');
['mousemove', 'mousedown'].forEach(e => document.addEventListener(e, wake));
document.addEventListener('mouseleave', () => { if (!v.paused && !busy()) document.body.classList.remove('active'); });
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

// --- the custom controls ---
const pad2 = n => String(n).padStart(2, '0');
function hms(s) {
  s = Math.max(0, Math.floor(s || 0));
  const h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60;
  return (h ? h + ':' + pad2(m) : pad2(m)) + ':' + pad2(s % 60);
}
// slider makes el a horizontal slider: set(frac) as it is dragged or
// clicked, done() once let go, tip(frac) the text over the pointer.
function slider(el, set, done, tip) {
  const frac = e => { const r = el.getBoundingClientRect(); return Math.max(0, Math.min(1, (e.clientX - r.left) / r.width)); };
  const tipEl = el.querySelector('.tip');
  el.addEventListener('pointermove', e => {
    if (!tipEl || !tip) return;
    const f = frac(e); tipEl.textContent = tip(f); tipEl.style.left = (f * 100) + '%';
  });
  el.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    e.preventDefault(); el.setPointerCapture(e.pointerId); el.classList.add('drag'); set(frac(e));
  });
  el.addEventListener('pointermove', e => { if (el.classList.contains('drag')) set(frac(e)); });
  const end = e => { if (!el.classList.contains('drag')) return; el.classList.remove('drag'); if (done) done(frac(e)); wake(); };
  el.addEventListener('pointerup', end); el.addEventListener('pointercancel', end);
}
const setBar = (el, f) => {
  f = Math.max(0, Math.min(1, f || 0)) * 100 + '%';
  el.querySelector('.fill').style.width = f; el.querySelector('.knob').style.left = f;
};
let scrub = -1; // the time being dragged to
function paintTime() {
  const d = isFinite(v.duration) ? v.duration : 0, t = scrub >= 0 ? scrub : v.currentTime;
  $('tcur').textContent = hms(t); $('tdur').textContent = hms(d);
  setBar($('prog'), d ? t / d : 0);
  let b = 0;
  for (let i = 0; i < v.buffered.length; i++) if (v.buffered.start(i) <= t + 0.5) b = Math.max(b, v.buffered.end(i));
  $('prog').querySelector('.buf').style.width = (d ? 100 * b / d : 0) + '%';
}
function paintVolume() {
  document.body.classList.toggle('muted', v.muted || v.volume === 0);
  setBar($('volbar'), v.muted ? 0 : v.volume);
}
function paintSpeed() {
  const r = v.playbackRate;
  $('spd').textContent = (Math.round(r * 100) / 100) + 'x';
  for (const b of $('spdmenu').children) b.classList.toggle('on', Math.abs(+b.dataset.r - r) < 0.01);
}
function togglePlay() { if (!v.getAttribute('src')) return; if (v.paused) v.play().catch(() => {}); else v.pause(); }
function setupControls() {
  document.body.classList.add('custom');
  v.controls = false;
  const bar = $('bar');
  bar.addEventListener('mouseenter', () => { overBar = true; wake(); });
  bar.addEventListener('mouseleave', () => { overBar = false; wake(); });
  // Buttons take no focus, so that Space and the arrows stay the player's.
  bar.addEventListener('mousedown', e => { if (e.target.closest('button')) e.preventDefault(); });
  bar.addEventListener('dblclick', e => e.stopPropagation());
  $('play').onclick = togglePlay;
  $('back').onclick = () => seek(-10);
  $('fwd').onclick = () => seek(10);
  $('cprev').onclick = () => step('prev');
  $('cnext').onclick = () => step('next');
  $('fs').onclick = toggleFS;
  $('mute').onclick = () => { if (v.muted || v.volume === 0) { v.muted = false; if (!v.volume) v.volume = 0.5; } else v.muted = true; };
  if (document.pictureInPictureEnabled && v.requestPictureInPicture) {
    $('pip').onclick = () => (document.pictureInPictureElement ? document.exitPictureInPicture() : v.requestPictureInPicture()).catch(() => {});
  } else $('pip').remove();
  const menu = $('spdmenu');
  for (const r of SPEEDS.slice().reverse()) {
    const b = document.createElement('button');
    b.dataset.r = r; b.textContent = r + 'x';
    b.onclick = () => { v.playbackRate = r; store.set('speed', r); menu.classList.remove('open'); toast(r + 'x 倍速'); };
    menu.appendChild(b);
  }
  $('spd').onclick = e => { e.stopPropagation(); menu.classList.toggle('open'); paintSpeed(); };
  document.addEventListener('pointerdown', e => { if (!e.target.closest('.wrap')) menu.classList.remove('open'); });
  slider($('prog'), f => { if (v.duration) { scrub = f * v.duration; paintTime(); } },
    f => { if (v.duration) v.currentTime = Math.min(v.duration - 0.1, f * v.duration); scrub = -1; paintTime(); },
    f => hms(f * (v.duration || 0)));
  slider($('volbar'), f => { v.muted = false; v.volume = Math.round(f * 100) / 100; });
  // A click on the picture plays or pauses; a double click is full screen.
  v.addEventListener('click', togglePlay);
  ['timeupdate', 'durationchange', 'progress', 'seeked', 'emptied', 'loadedmetadata'].forEach(e => v.addEventListener(e, paintTime));
  v.addEventListener('volumechange', paintVolume);
  v.addEventListener('ratechange', paintSpeed);
  const playing = () => document.body.classList.toggle('playing', !v.paused);
  v.addEventListener('play', playing); v.addEventListener('pause', playing);
  const nav = refreshNav;
  refreshNav = () => { nav(); $('cprev').disabled = !hasPrev(); $('cnext').disabled = !hasNext(); };
  paintTime(); paintVolume(); paintSpeed(); refreshNav();
}

v.volume = store.get('volume', 1); v.muted = MUTED || store.get('muted', false);
if (CUSTOM) setupControls();
refreshNav();
fetch('/current').then(r => r.json()).then(load).catch(() => {});
</script>
</body></html>`
