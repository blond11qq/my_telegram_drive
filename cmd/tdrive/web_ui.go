package main

// webUIHTML is a Windows-11-Explorer-style browser client: navigation buttons
// with history, editable address bar, search, folder tree sidebar, details and
// icon views with sortable columns, multi-select, clipboard cut/copy/paste,
// context menus, keyboard shortcuts, details pane, properties, streaming
// previews, downloads, and two-stage uploads.
const webUIHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TDrive</title>
<style>
:root { color-scheme: dark; --bg:#191919; --panel:#2b2b2b; --panel2:#202020; --line:#353535; --fg:#f3f3f3; --dim:#a6a6a6; --acc:#4c8dff; --sel:#375079; }
* { box-sizing: border-box; }
html, body { height:100%; }
body { margin:0; background:var(--bg); color:var(--fg); font:13px/1.5 "Segoe UI", system-ui, sans-serif; display:flex; flex-direction:column; overflow:hidden; }
button { background:transparent; color:var(--fg); border:1px solid transparent; border-radius:6px; padding:6px 10px; font-size:13px; cursor:pointer; display:inline-flex; align-items:center; gap:6px; }
button:hover { background:#3a3a3a; }
button:disabled { opacity:.4; cursor:default; background:transparent; }
button.primary { background:var(--acc); color:#fff; }
button.primary:hover { background:#3d78e7; }
input[type=text], input[type=search] { background:#101010; color:var(--fg); border:1px solid var(--line); border-radius:6px; padding:6px 10px; font-size:13px; }
#navbar { display:flex; gap:4px; align-items:center; padding:8px 10px 4px; }
#navbar .navbtn { font-size:15px; padding:6px 9px; }
#address { flex:1; display:flex; align-items:center; gap:2px; background:#101010; border:1px solid var(--line); border-radius:6px; padding:4px 8px; min-width:0; overflow:hidden; white-space:nowrap; }
#address a { color:var(--fg); text-decoration:none; padding:3px 6px; border-radius:4px; cursor:pointer; }
#address a:hover { background:#3a3a3a; }
#address input { flex:1; background:transparent; border:none; outline:none; }
#search { width:220px; }
#ribbon { display:flex; gap:2px; align-items:center; padding:4px 10px 8px; flex-wrap:wrap; }
#ribbon .sep { width:1px; height:22px; background:var(--line); margin:0 6px; }
#ribbon .menuwrap { position:relative; }
.menu { position:absolute; top:100%; left:0; background:var(--panel); border:1px solid var(--line); border-radius:8px; min-width:180px; padding:4px; z-index:40; box-shadow:0 8px 24px rgba(0,0,0,.5); }
.menu button { display:flex; width:100%; text-align:left; }
.menu button .tick { width:18px; }
#body { flex:1; display:flex; min-height:0; }
#side { width:230px; min-width:170px; border-right:1px solid var(--line); background:var(--panel2); display:flex; flex-direction:column; }
#sidetree { flex:1; overflow:auto; padding:6px 4px; }
#sidebottom { border-top:1px solid var(--line); padding:6px; }
#sidebottom button { width:100%; justify-content:flex-start; }
#modal .sect { margin:0 0 14px; }
#modal .sect h4 { margin:0 0 8px; font-size:13px; color:var(--acc); }
#modal .kv { display:flex; gap:10px; padding:3px 0; font-size:13px; }
#modal .kv .k { width:110px; color:var(--dim); flex-shrink:0; }
#modal .kv .v { word-break:break-all; }
#modal select { background:#101010; color:var(--fg); border:1px solid var(--line); border-radius:6px; padding:6px 8px; }
#modal label.chk { display:flex; gap:8px; align-items:center; font-size:13px; }
#movelist { max-height:40vh; overflow:auto; border:1px solid var(--line); border-radius:8px; margin:8px 0; }
#movelist .mrow { display:flex; gap:8px; align-items:center; padding:7px 10px; cursor:pointer; }
#movelist .mrow:hover { background:#3a3a3a; }
#movelist .mrow.cur { background:var(--sel); }
#movecrumbs { color:var(--dim); font-size:12px; }
#movecrumbs a { color:var(--fg); cursor:pointer; text-decoration:none; }
#tokbox { display:flex; gap:6px; }
#tokbox input { flex:1; }
.tnode { user-select:none; }
.trow { display:flex; align-items:center; gap:4px; padding:4px 6px; border-radius:5px; cursor:pointer; white-space:nowrap; }
.trow:hover { background:#3a3a3a; }
.trow.cur { background:var(--sel); }
.trow .tw { width:16px; color:var(--dim); font-size:10px; }
.tkids { margin-left:16px; }
#maincol { flex:1; display:flex; flex-direction:column; min-width:0; }
#colhead { display:flex; gap:8px; padding:6px 12px; color:var(--dim); border-bottom:1px solid var(--line); font-size:12px; }
#colhead span { cursor:pointer; user-select:none; }
#colhead span:hover { color:var(--fg); }
#files { flex:1; overflow:auto; padding:4px 6px; }
#files.icons { display:flex; flex-wrap:wrap; gap:4px; align-content:flex-start; }
.row { display:flex; align-items:center; gap:8px; padding:5px 8px; border-radius:5px; border:1px solid transparent; cursor:default; user-select:none; }
.row:hover { background:#2e2e2e; }
.row.sel { background:var(--sel); border-color:#4a6a99; }
.row.cut { opacity:.45; }
.row .nm { flex:1; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.row .cb { accent-color:var(--acc); }
.row .c2, .row .c3, .row .c4 { color:var(--dim); font-size:12px; white-space:nowrap; }
.row .c2 { width:150px; } .row .c3 { width:90px; text-align:right; } .row .c4 { width:130px; }
.tile { width:104px; padding:10px 6px; display:flex; flex-direction:column; align-items:center; gap:6px; border-radius:6px; border:1px solid transparent; cursor:default; user-select:none; text-align:center; }
.tile:hover { background:#2e2e2e; }
.tile.sel { background:var(--sel); border-color:#4a6a99; }
.tile.cut { opacity:.45; }
.tile .ic { font-size:34px; }
.tile .nm { font-size:12px; word-break:break-word; max-height:3.2em; overflow:hidden; }
.tile .cb { position:absolute; margin:2px 0 0 -38px; accent-color:var(--acc); }
#statusbar { display:flex; gap:14px; padding:5px 12px; border-top:1px solid var(--line); color:var(--dim); font-size:12px; }
#details { width:260px; border-left:1px solid var(--line); overflow:auto; padding:12px; background:var(--panel2); }
#details .pv { text-align:center; margin-bottom:10px; }
#details img, #details video { max-width:100%; max-height:180px; }
#details dl { margin:0; font-size:12px; }
#details dt { color:var(--dim); margin-top:8px; }
#details dd { margin:2px 0 0; word-break:break-all; }
#ctxmenu { position:fixed; background:var(--panel); border:1px solid var(--line); border-radius:8px; padding:4px; z-index:60; min-width:200px; box-shadow:0 8px 24px rgba(0,0,0,.5); display:none; }
#ctxmenu button { display:flex; width:100%; text-align:left; }
#ctxmenu .sep { height:1px; background:var(--line); margin:4px 6px; }
#modalWrap { position:fixed; inset:0; background:rgba(0,0,0,.6); display:none; align-items:center; justify-content:center; z-index:70; }
#modalWrap.open { display:flex; }
#modal { background:var(--panel); border:1px solid var(--line); border-radius:12px; padding:18px; width:min(440px,92vw); }
#modal h3 { margin:0 0 12px; font-size:15px; }
#modal input { width:100%; margin-bottom:12px; }
#modal .btns { display:flex; justify-content:flex-end; gap:8px; }
#modal .props { font-size:13px; }
#modal .props .pr { display:flex; gap:10px; padding:4px 0; border-bottom:1px solid var(--line); }
#modal .props .k { width:90px; color:var(--dim); flex-shrink:0; }
#modal .props .v { word-break:break-all; }
#preview { position:fixed; inset:0; background:rgba(0,0,0,.88); display:none; align-items:center; justify-content:center; flex-direction:column; gap:10px; z-index:65; padding:20px; }
#preview.open { display:flex; }
#preview video, #preview img, #preview iframe { max-width:92vw; max-height:74vh; background:#000; }
#preview pre { max-width:92vw; max-height:74vh; overflow:auto; background:#000; padding:12px; width:80vw; }
#pvbar { display:flex; align-items:center; gap:8px; }
#pvcap { color:var(--dim); font-size:12px; max-width:40vw; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
#drop { position:fixed; inset:0; border:3px dashed var(--acc); display:none; align-items:center; justify-content:center; font-size:20px; background:rgba(20,22,26,.9); z-index:80; }
#drop.on { display:flex; }
#uploads { position:fixed; right:14px; bottom:44px; width:min(380px,90vw); background:var(--panel); border:1px solid var(--line); border-radius:10px; padding:10px 12px; display:none; z-index:55; max-height:50vh; overflow:auto; }
#uploads.open { display:block; }
#uploads h3 { margin:0 0 8px; font-size:13px; }
.ufile { margin-bottom:10px; font-size:12px; }
.ufile .uname { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.stage { display:flex; align-items:center; gap:6px; margin-top:3px; }
.stage .lbl { width:96px; color:var(--dim); white-space:nowrap; }
.stage progress { flex:1; height:8px; }
.stage .pct { width:64px; text-align:right; color:var(--dim); }
#toast { position:fixed; bottom:44px; left:14px; color:var(--dim); font-size:12px; background:var(--panel); border:1px solid var(--line); padding:6px 12px; border-radius:8px; }
#fileInput { display:none; }
</style>
</head>
<body>
<div id="navbar">
<button class="navbtn" id="btnBack" title="뒤로">←</button>
<button class="navbtn" id="btnFwd" title="앞으로">→</button>
<button class="navbtn" id="btnUp" title="위로">↑</button>
<button class="navbtn" id="btnRefresh" title="새로 고침">⟳</button>
<div id="address"></div>
<input type="search" id="search" placeholder="검색">
</div>
<div id="ribbon">
<button id="btnNew">📁 새로 만들기</button>
<span class="sep"></span>
<button id="btnCut">✂ 잘라내기</button>
<button id="btnCopy">⧉ 복사</button>
<button id="btnPaste">📋 붙여넣기</button>
<button id="btnMoveTo">➦ 이동…</button>
<button id="btnRename">✎ 이름 바꾸기</button>
<button id="btnDelete">🗑 삭제</button>
<span class="sep"></span>
<button id="btnUpload" class="primary">⬆ 업로드</button>
<button id="btnUploadDir">📁 폴더 업로드</button>
<button id="btnDownload">⬇ 다운로드</button>
<button id="btnProps">📄 속성</button>
<span class="sep"></span>
<div class="menuwrap"><button id="btnView">▦ 보기 ▾</button><div class="menu" id="menuView" hidden>
<button data-view="details"><span class="tick"></span>자세히</button>
<button data-view="icons"><span class="tick"></span>큰 아이콘</button>
</div></div>
<div class="menuwrap"><button id="btnSort">⇅ 정렬 ▾</button><div class="menu" id="menuSort" hidden>
<button data-sort="name"><span class="tick"></span>이름</button>
<button data-sort="date"><span class="tick"></span>수정한 날짜</button>
<button data-sort="type"><span class="tick"></span>유형</button>
<button data-sort="size"><span class="tick"></span>크기</button>
</div></div>
<button id="btnDetails">🔍 세부 정보 창</button>
</div>
<div id="body">
<nav id="side"><div id="sidetree"></div><div id="sidebottom"><button id="btnSettings">⚙ 설정</button></div></nav>
<div id="maincol">
<div id="colhead"></div>
<div id="files"></div>
<div id="statusbar"></div>
</div>
<aside id="details" hidden></aside>
</div>
<div id="ctxmenu"></div>
<div id="modalWrap"><div id="modal"></div></div>
<div id="preview"><div id="stage"></div><div id="pvbar"><button id="pvPrev">◀ 이전</button><span id="pvcap"></span><a id="dl" href="#"><button>다운로드</button></a><button id="pvNext">다음 ▶</button> <button id="closePv">닫기</button></div></div>
<div id="drop">여기에 파일을 놓으면 업로드됩니다</div>
<div id="uploads"><h3>업로드</h3><div id="ulist"></div></div>
<div id="toast"></div>
<input type="file" id="fileInput" multiple>
<input type="file" id="dirInput" webkitdirectory>
<script>
"use strict";
var token = new URLSearchParams(location.search).get("token") || "";
function withToken(u) {
  if (!token) return u;
  return u + (u.indexOf("?") >= 0 ? "&" : "?") + "token=" + encodeURIComponent(token);
}
function $(id) { return document.getElementById(id); }
function toast(msg) { $("toast").textContent = msg; }

var state = {
  cwd: "/", entries: [], hist: ["/"], hidx: 0,
  sortKey: "name", sortDir: 1, view: "details",
  sel: [], anchor: -1, clip: null, detailsPane: false, searching: ""
};

var TYPE_LABELS = {pdf:"PDF 문서", mp4:"MP4 비디오", m4v:"M4V 비디오", mov:"MOV 비디오", mkv:"MKV 비디오", webm:"WebM 비디오", avi:"AVI 비디오", mp3:"MP3 오디오", m4a:"M4A 오디오", wav:"WAV 오디오", flac:"FLAC 오디오", ogg:"OGG 오디오", jpg:"JPEG 이미지", jpeg:"JPEG 이미지", png:"PNG 이미지", gif:"GIF 이미지", webp:"WebP 이미지", bmp:"BMP 이미지", txt:"텍스트 문서", md:"Markdown 문서", log:"로그 파일", csv:"CSV 파일", json:"JSON 파일", xml:"XML 파일", zip:"ZIP 압축 파일", tgz:"GZIP 압축 파일", gz:"GZIP 압축 파일", "7z":"7-Zip 압축 파일", rar:"RAR 압축 파일", exe:"응용 프로그램", msi:"Windows 설치 관리자", apk:"Android 패키지", ipa:"iOS 앱", docx:"Word 문서", xlsx:"Excel 문서", pptx:"PowerPoint 문서", hwp:"한글 문서", srt:"자막 파일", vtt:"자막 파일"};
var TYPE_ICONS = {pdf:"📕", mp4:"🎬", m4v:"🎬", mov:"🎬", mkv:"🎬", webm:"🎬", avi:"🎬", mp3:"🎵", m4a:"🎵", wav:"🎵", flac:"🎵", ogg:"🎵", jpg:"🖼", jpeg:"🖼", png:"🖼", gif:"🖼", webp:"🖼", bmp:"🖼", txt:"📝", md:"📝", zip:"📦", gz:"📦", tgz:"📦", "7z":"📦", rar:"📦", exe:"⚙", apk:"🤖", ipa:"🍎"};

function extOf(name) { var i = name.lastIndexOf("."); return i < 0 ? "" : name.slice(i + 1).toLowerCase(); }
function iconOf(e) { if (e.type === "folder") return "📁"; return TYPE_ICONS[extOf(e.name)] || "📄"; }
function typeOf(e) { if (e.type === "folder") return "파일 폴더"; var l = TYPE_LABELS[extOf(e.name)]; return l || ((extOf(e.name) || "알 수 없는").toUpperCase() + " 파일"); }
function fmtSize(n) {
  if (n == null || n === 0) return n === 0 ? "0 바이트" : "";
  if (n < 1024) return n + " 바이트";
  var units = ["KB","MB","GB","TB"], v = n, u = "바이트";
  for (var i = 0; i < units.length; i++) { v /= 1024; u = units[i]; if (v < 1024) break; }
  return (Math.round(v * 10) / 10) + " " + u;
}
function fmtSizeShort(n) {
  if (!n) return "";
  if (n < 1024) return n + " B";
  var units = ["KB","MB","GB","TB"], v = n;
  for (var i = 0; i < units.length; i++) { v /= 1024; if (v < 1024) return (Math.round(v * 10) / 10) + " " + units[i]; }
  return v + " PB";
}
function fmtTime(t) { return t ? new Date(t * 1000).toLocaleString() : ""; }

async function api(url, opts) {
  var r = await fetch(withToken(url), opts);
  var j = await r.json().catch(function() { return {}; });
  if (!r.ok || j.ok === false) throw new Error(j.error || ("HTTP " + r.status));
  return j;
}

/* ---------- navigation (synced with browser history) ---------- */
function pageURL(p) {
  var path = p || state.cwd || "/";
  var base = token ? "/?token=" + encodeURIComponent(token) : "/";
  return base + (base.indexOf("?") >= 0 ? "&" : "?") + "path=" + encodeURIComponent(path);
}
function pathFromURL() {
  try { return new URLSearchParams(location.search).get("path") || "/"; }
  catch (e) { return "/"; }
}
async function load(dir, push) {
  dir = dir || "/";
  if (push === undefined) push = true;
  state.searching = "";
  $("search").value = "";
  toast("불러오는 중…");
  try {
    var j = await api("/api/list?path=" + encodeURIComponent(dir));
    state.cwd = j.path || dir;
    state.entries = j.entries || [];
    state.sel = []; state.anchor = -1;
    if (push) {
      state.hist = state.hist.slice(0, state.hidx + 1);
      state.hist.push(state.cwd);
      state.hidx++;
      try { history.pushState({p: state.cwd}, "", pageURL(state.cwd)); } catch (e) {}
    } else {
      try { history.replaceState({p: state.cwd}, "", pageURL(state.cwd)); } catch (e) {}
    }
    renderAll();
    toast(state.entries.length + "개 항목");
  } catch (e) { toast("오류: " + e.message); }
}
function goHist(delta) {
  try { history.go(delta); } catch (e) {}
}
function syncHistToURL() {
  var p = pathFromURL();
  var i = state.hist.lastIndexOf(p);
  if (i >= 0) state.hidx = i;
  else { state.hist.push(p); state.hidx = state.hist.length - 1; }
  load(p, false);
}
function goUp() {
  var p = state.cwd.split("/").filter(Boolean); p.pop();
  load("/" + p.join("/"));
}

/* ---------- address bar ---------- */
function renderAddress() {
  var bar = $("address"); bar.textContent = "";
  if (state.searching) { bar.textContent = "🔍 검색 결과: " + state.searching; return; }
  var parts = state.cwd.split("/").filter(Boolean);
  var home = document.createElement("a"); home.textContent = "⌂"; home.title = "홈";
  home.onclick = function() { load("/"); };
  bar.appendChild(home);
  var acc = "";
  parts.forEach(function(p) {
    acc += "/" + p;
    var sep = document.createElement("span"); sep.textContent = " › "; sep.style.color = "#888";
    bar.appendChild(sep);
    (function(target) {
      var a = document.createElement("a"); a.textContent = p;
      a.onclick = function() { load(target); };
      bar.appendChild(a);
    })(acc);
  });
  var edit = document.createElement("a"); edit.textContent = "✎"; edit.title = "경로 직접 입력";
  edit.style.marginLeft = "auto";
  edit.onclick = editAddress;
  bar.appendChild(edit);
}
function editAddress() {
  var bar = $("address"); bar.textContent = "";
  var input = document.createElement("input");
  input.type = "text"; input.value = state.cwd;
  input.onkeydown = function(ev) {
    if (ev.key === "Enter") load(input.value || "/");
    if (ev.key === "Escape") renderAddress();
  };
  input.onblur = renderAddress;
  bar.appendChild(input);
  input.focus(); input.select();
}

/* ---------- sidebar tree ---------- */
var expanded = {"/": true};
async function renderSide() {
  var side = $("sidetree"); side.textContent = "";
  side.appendChild(treeNode("/", "⌂ 홈", 0));
}
function treeNode(dir, label, depth) {
  var wrap = document.createElement("div"); wrap.className = "tnode";
  var row = document.createElement("div"); row.className = "trow" + (state.cwd === dir ? " cur" : "");
  var tw = document.createElement("span"); tw.className = "tw";
  var kids = document.createElement("div"); kids.className = "tkids";
  function kidsOf(cb) { api("/api/list?path=" + encodeURIComponent(dir)).then(function(j) { cb((j.entries || []).filter(function(e) { return e.type === "folder"; })); }).catch(function() { cb([]); }); }
  tw.textContent = "▸";
  tw.onclick = function(ev) {
    ev.stopPropagation();
    if (expanded[dir]) { delete expanded[dir]; kids.textContent = ""; tw.textContent = "▸"; }
    else { expanded[dir] = true; tw.textContent = "▾"; kidsOf(function(folders) { folders.forEach(function(f) { kids.appendChild(treeNode(f.path, f.name, depth + 1)); }); }); }
  };
  var ic = document.createElement("span"); ic.textContent = dir === "/" ? "⌂" : "📁";
  var nm = document.createElement("span"); nm.textContent = label;
  row.appendChild(tw); row.appendChild(ic); row.appendChild(nm);
  row.onclick = function() { load(dir); };
  wrap.appendChild(row); wrap.appendChild(kids);
  if (expanded[dir] && dir !== undefined) {
    tw.textContent = "▾";
    kidsOf(function(folders) { folders.forEach(function(f) { kids.appendChild(treeNode(f.path, f.name, depth + 1)); }); });
  }
  return wrap;
}

/* ---------- preferences ---------- */
var prefs = {view: "details", sortKey: "name", sortDir: 1, detailsPane: false, encryptUpload: true};
function loadPrefs() {
  try {
    var saved = JSON.parse(localStorage.getItem("tdrive-prefs") || "{}");
    for (var k in prefs) if (saved[k] !== undefined) prefs[k] = saved[k];
  } catch (e) {}
  state.view = prefs.view; state.sortKey = prefs.sortKey;
  state.sortDir = prefs.sortDir; state.detailsPane = !!prefs.detailsPane;
}
function savePrefs() {
  prefs.view = state.view; prefs.sortKey = state.sortKey;
  prefs.sortDir = state.sortDir; prefs.detailsPane = state.detailsPane;
  try { localStorage.setItem("tdrive-prefs", JSON.stringify(prefs)); } catch (e) {}
}
function saveEncryptPref(v) {
  prefs.encryptUpload = !!v;
  try { localStorage.setItem("tdrive-prefs", JSON.stringify(prefs)); } catch (e) {}
}
function sortedEntries() {
  var list = state.entries.slice();
  var k = state.sortKey, d = state.sortDir;
  function val(e) {
    if (k === "name") return e.name.toLowerCase();
    if (k === "size") return e.type === "folder" ? -1 : (e.size || 0);
    if (k === "date") return e.upload_time || 0;
    if (k === "type") return typeOf(e);
    return e.name.toLowerCase();
  }
  list.sort(function(a, b) {
    if ((a.type === "folder") !== (b.type === "folder")) return a.type === "folder" ? -1 : 1;
    var x = val(a), y = val(b);
    if (x < y) return -d; if (x > y) return d; return 0;
  });
  return list;
}
var SORT_LABELS = {name: "이름", date: "수정한 날짜", type: "유형", size: "크기"};
function renderColHead() {
  var head = $("colhead"); head.textContent = "";
  if (state.view !== "details") return;
  var all = document.createElement("input");
  all.type = "checkbox"; all.className = "cb"; all.title = "모두 선택";
  all.checked = state.sel.length > 0 && state.sel.length === state.entries.length;
  all.onclick = function() { state.sel = all.checked ? state.entries.map(function(e) { return e.path; }) : []; renderFiles(); renderStatus(); };
  head.appendChild(all);
  ["name", "date", "type", "size"].forEach(function(k) {
    var s = document.createElement("span");
    var arrow = state.sortKey === k ? (state.sortDir > 0 ? " ▲" : " ▼") : "";
    s.textContent = SORT_LABELS[k] + arrow;
    s.style.flex = k === "name" ? "1" : "";
    s.style.width = k === "name" ? "" : (k === "date" ? "130px" : (k === "type" ? "150px" : "90px"));
    if (k === "size") s.style.textAlign = "right";
    s.onclick = function() {
      if (state.sortKey === k) state.sortDir = -state.sortDir;
      else { state.sortKey = k; state.sortDir = 1; }
      savePrefs();
      renderFiles(); renderColHead(); syncMenus();
    };
    head.appendChild(s);
  });
}

/* ---------- selection ---------- */
function isSel(p) { return state.sel.indexOf(p) >= 0; }
function clickSelect(ev, e, idx, list) {
  if (ev.shiftKey && state.anchor >= 0) {
    var a = Math.min(state.anchor, idx), b = Math.max(state.anchor, idx);
    state.sel = list.slice(a, b + 1).map(function(x) { return x.path; });
  } else if (ev.ctrlKey || ev.metaKey) {
    if (isSel(e.path)) state.sel = state.sel.filter(function(p) { return p !== e.path; });
    else state.sel.push(e.path);
    state.anchor = idx;
  } else {
    state.sel = [e.path]; state.anchor = idx;
  }
}
function selectedEntries() {
  var map = {};
  state.entries.forEach(function(e) { map[e.path] = e; });
  return state.sel.map(function(p) { return map[p]; }).filter(Boolean);
}

/* ---------- file area ---------- */
function renderFiles() {
  var box = $("files"); box.textContent = "";
  box.className = state.view === "icons" ? "icons" : "";
  box.id = "files";
  var list = state.searching ? state.entries : sortedEntries();
  state.viewList = list;
  if (!list.length) { box.textContent = state.searching ? "검색 결과 없음" : "비어 있음"; return; }
  list.forEach(function(e, idx) {
    if (state.view === "icons") box.appendChild(tile(e, idx, list));
    else box.appendChild(row(e, idx, list));
  });
}
function checkBox(e) {
  var cb = document.createElement("input");
  cb.type = "checkbox"; cb.className = "cb";
  cb.checked = isSel(e.path);
  cb.onclick = function(ev) {
    ev.stopPropagation();
    if (cb.checked && !isSel(e.path)) state.sel.push(e.path);
    if (!cb.checked) state.sel = state.sel.filter(function(p) { return p !== e.path; });
    renderFiles(); renderStatus();
  };
  return cb;
}
function row(e, idx, list) {
  var r = document.createElement("div");
  r.className = "row" + (isSel(e.path) ? " sel" : "") + (state.clip && state.clip.mode === "cut" && state.clip.items.indexOf(e.path) >= 0 ? " cut" : "");
  r.appendChild(checkBox(e));
  var ic = document.createElement("span"); ic.textContent = iconOf(e); ic.style.fontSize = "18px";
  var nm = document.createElement("span"); nm.className = "nm"; nm.textContent = e.name; nm.title = e.name;
  var c2 = document.createElement("span"); c2.className = "c2"; c2.textContent = typeOf(e);
  var c3 = document.createElement("span"); c3.className = "c3"; c3.textContent = e.type === "folder" ? "" : fmtSizeShort(e.size);
  var c4 = document.createElement("span"); c4.className = "c4"; c4.textContent = fmtTime(e.upload_time);
  r.appendChild(ic); r.appendChild(nm); r.appendChild(c4); r.appendChild(c2); r.appendChild(c3);
  r.onclick = function(ev) { clickSelect(ev, e, idx, list); renderFiles(); renderStatus(); renderDetails(); };
  r.ondblclick = function() { openEntry(e); };
  r.oncontextmenu = function(ev) { ev.preventDefault(); if (!isSel(e.path)) { state.sel = [e.path]; renderFiles(); } showCtx(ev.clientX, ev.clientY, "items"); };
  return r;
}
function tile(e, idx, list) {
  var t = document.createElement("div");
  t.className = "tile" + (isSel(e.path) ? " sel" : "") + (state.clip && state.clip.mode === "cut" && state.clip.items.indexOf(e.path) >= 0 ? " cut" : "");
  t.style.position = "relative";
  t.appendChild(checkBox(e));
  var ic = document.createElement("div"); ic.className = "ic"; ic.textContent = iconOf(e);
  var nm = document.createElement("div"); nm.className = "nm"; nm.textContent = e.name; nm.title = e.name;
  t.appendChild(ic); t.appendChild(nm);
  t.onclick = function(ev) { clickSelect(ev, e, idx, list); renderFiles(); renderStatus(); renderDetails(); };
  t.ondblclick = function() { openEntry(e); };
  t.oncontextmenu = function(ev) { ev.preventDefault(); if (!isSel(e.path)) { state.sel = [e.path]; renderFiles(); } showCtx(ev.clientX, ev.clientY, "items"); };
  return t;
}
function openEntry(e) {
  if (e.type === "folder") load(e.path);
  else preview(e);
}

/* ---------- status bar / details pane ---------- */
function renderStatus() {
  var total = state.entries.filter(function(e) { return e.type === "file"; }).reduce(function(a, e) { return a + (e.size || 0); }, 0);
  var selSize = selectedEntries().filter(function(e) { return e.type === "file"; }).reduce(function(a, e) { return a + (e.size || 0); }, 0);
  var t = state.entries.length + "개 항목";
  if (state.sel.length) t += "  |  " + state.sel.length + "개 선택함 (" + fmtSize(selSize) + ")";
  else if (total) t += "  |  합계 " + fmtSize(total);
  if (state.clip && state.clip.items.length) t += "  |  " + (state.clip.mode === "cut" ? "잘라내기" : "복사") + " " + state.clip.items.length + "개 — 붙여넣기 대기";
  $("statusbar").textContent = t;
}
function renderDetails() {
  var pane = $("details");
  if (!state.detailsPane) { pane.hidden = true; return; }
  pane.hidden = false; pane.textContent = "";
  var sel = selectedEntries();
  if (!sel.length) { pane.textContent = "항목을 선택하면 세부 정보가 표시됩니다."; return; }
  if (sel.length > 1) { pane.textContent = sel.length + "개 항목 선택함"; return; }
  var e = sel[0];
  var pv = document.createElement("div"); pv.className = "pv";
  var ext = extOf(e.name);
  if (["jpg","jpeg","png","gif","webp","bmp"].indexOf(ext) >= 0) {
    var img = document.createElement("img"); img.src = fileURL(e, false); pv.appendChild(img);
  } else if (["mp4","m4v","mov","webm"].indexOf(ext) >= 0) {
    var v = document.createElement("video"); v.src = fileURL(e, false); v.preload = "metadata"; v.muted = true; pv.appendChild(v);
  } else {
    var big = document.createElement("div"); big.style.fontSize = "52px"; big.textContent = iconOf(e); pv.appendChild(big);
  }
  pane.appendChild(pv);
  var dl = document.createElement("dl");
  [["이름", e.name], ["유형", typeOf(e)], ["위치", e.path], ["크기", e.type === "folder" ? "" : fmtSize(e.size)], ["수정한 날짜", fmtTime(e.upload_time)]].forEach(function(pair) {
    var dt = document.createElement("dt"); dt.textContent = pair[0];
    var dd = document.createElement("dd"); dd.textContent = pair[1];
    dl.appendChild(dt); dl.appendChild(dd);
  });
  pane.appendChild(dl);
}

/* ----- preview gallery: arrows step across previewable siblings ----- */
var PREVIEWABLE = ["mp4","m4v","mov","webm","mkv","avi","mp3","m4a","aac","wav","flac","ogg","opus","jpg","jpeg","png","gif","webp","bmp","pdf","txt","log","md","csv","json","xml","srt","vtt"];
function previewable(e) { return e.type === "file" && PREVIEWABLE.indexOf(extOf(e.name)) >= 0; }
var pvList = [], pvIdx = -1;
function fileURL(e, download) {
  return withToken("/file?path=" + encodeURIComponent(e.path) + (download ? "&download=1" : ""));
}
function proxyHLSURL(e, f) {
  return withToken("/proxy-hls?path=" + encodeURIComponent(e.path) + "&f=" + f);
}
/* HLS upgrade is opportunistic: the native mp4 proxy stays the source until
   an HLS mapping proves available, and every failure below keeps it. Safari
   takes the playlist directly; other browsers get hls.js from CDN, and a CDN
   outage simply never replaces the working native source. */
function watchProxyHLS(el, e) {
  fetch(withToken("/api/proxy/status?path=" + encodeURIComponent(e.path))).then(function(r) { return r.json(); }).then(function(s) {
    if (!s || !s.ok || !s.job || !s.job.has_hls) return;
    var pl = proxyHLSURL(e, "playlist");
    if (el.canPlayType && el.canPlayType("application/vnd.apple.mpegurl")) { el.src = pl; return; }
    if (window.Hls) { attachHls(el, pl); return; }
    var sc = document.createElement("script");
    sc.src = "https://cdn.jsdelivr.net/npm/hls.js@1";
    sc.onload = function() { attachHls(el, pl); };
    document.head.appendChild(sc);
  }).catch(function() {});
}
function attachHls(el, pl) {
  try {
    if (!window.Hls || !window.Hls.isSupported()) return;
    if (!el.dataset.mp4) el.dataset.mp4 = el.src;
    var hls = new window.Hls();
    hls.on(window.Hls.Events.ERROR, function(ev, data) {
      if (data && data.fatal) { try { hls.destroy(); } catch (_) {} el.src = el.dataset.mp4; }
    });
    hls.loadSource(pl);
    hls.attachMedia(el);
  } catch (_) {}
}
function preview(e) {
  pvList = (state.viewList || []).filter(previewable);
  pvIdx = -1;
  for (var i = 0; i < pvList.length; i++) if (pvList[i].path === e.path) pvIdx = i;
  if (pvIdx < 0) { pvList = [e]; pvIdx = 0; }
  showPv();
  $("preview").classList.add("open");
}
function pvStep(d) {
  if (pvList.length < 2) return;
  pvIdx = (pvIdx + d + pvList.length) % pvList.length;
  showPv();
}
function showPv() {
  var e = pvList[pvIdx];
  var stage = $("stage"); stage.textContent = "";
  var ext = extOf(e.name);
  var url = fileURL(e, false);
  $("dl").href = fileURL(e, true);
  var el = null;
  if (["mp4","m4v","mov","webm","mkv","avi"].indexOf(ext) >= 0) { el = document.createElement("video"); el.controls = true; el.preload = "auto"; el.src = url; watchProxyHLS(el, e); }
  else if (["mp3","m4a","aac","wav","flac","ogg","opus"].indexOf(ext) >= 0) { el = document.createElement("audio"); el.controls = true; el.src = url; }
  else if (["jpg","jpeg","png","gif","webp","bmp"].indexOf(ext) >= 0) { el = document.createElement("img"); el.src = url; }
  else if (ext === "pdf") { el = document.createElement("iframe"); el.src = url; el.style.width = "80vw"; el.style.height = "78vh"; }
  else if (["txt","log","md","csv","json","xml","srt","vtt"].indexOf(ext) >= 0) {
    el = document.createElement("pre"); el.textContent = "불러오는 중…";
    fetch(url).then(function(r) { return r.text(); }).then(function(t) { el.textContent = t.slice(0, 200000); }).catch(function(err) { el.textContent = "오류: " + err; });
  } else {
    el = document.createElement("div"); el.textContent = "미리보기를 지원하지 않는 형식입니다. 다운로드하세요.";
  }
  stage.appendChild(el);
  var cap = e.name + (pvList.length > 1 ? "  (" + (pvIdx + 1) + " / " + pvList.length + ")" : "");
  $("pvcap").textContent = cap;
  $("pvcap").title = e.path;
  $("pvPrev").disabled = pvList.length < 2;
  $("pvNext").disabled = pvList.length < 2;
}

/* ---------- modal dialogs ---------- */
function closeModal() { $("modalWrap").classList.remove("open"); $("modal").textContent = ""; }
function modalInput(title, initial, okLabel, cb) {
  var m = $("modal"); m.textContent = "";
  var h = document.createElement("h3"); h.textContent = title; m.appendChild(h);
  var input = document.createElement("input"); input.type = "text"; input.value = initial || "";
  m.appendChild(input);
  var btns = document.createElement("div"); btns.className = "btns";
  var cancel = document.createElement("button"); cancel.textContent = "취소"; cancel.onclick = closeModal;
  var ok = document.createElement("button"); ok.textContent = okLabel || "확인"; ok.className = "primary";
  ok.onclick = function() { closeModal(); cb(input.value); };
  btns.appendChild(cancel); btns.appendChild(ok); m.appendChild(btns);
  $("modalWrap").classList.add("open");
  input.focus(); input.select();
  input.onkeydown = function(ev) { if (ev.key === "Enter") ok.onclick(); if (ev.key === "Escape") closeModal(); };
}
function modalConfirm(title, message, cb) {
  var m = $("modal"); m.textContent = "";
  var h = document.createElement("h3"); h.textContent = title; m.appendChild(h);
  var p = document.createElement("p"); p.textContent = message; m.appendChild(p);
  var btns = document.createElement("div"); btns.className = "btns";
  var cancel = document.createElement("button"); cancel.textContent = "취소"; cancel.onclick = closeModal;
  var ok = document.createElement("button"); ok.textContent = "삭제"; ok.className = "primary";
  ok.onclick = function() { closeModal(); cb(); };
  btns.appendChild(cancel); btns.appendChild(ok); m.appendChild(btns);
  $("modalWrap").classList.add("open");
}
/* ---------- settings ---------- */
function sect(title) {
  var s = document.createElement("div"); s.className = "sect";
  var h = document.createElement("h4"); h.textContent = title; s.appendChild(h);
  return s;
}
function kvRow(box, k, v) {
  var r = document.createElement("div"); r.className = "kv";
  var kk = document.createElement("span"); kk.className = "k"; kk.textContent = k;
  var vv = document.createElement("span"); vv.className = "v"; vv.textContent = v;
  r.appendChild(kk); r.appendChild(vv); box.appendChild(r);
}
async function openSettings() {
  var m = $("modal"); m.textContent = "";
  var h = document.createElement("h3"); h.textContent = "⚙ 설정"; m.appendChild(h);
  var conn = sect("연결"); m.appendChild(conn);
  kvRow(conn, "서버 주소", location.host);
  try {
    var st = (await api("/api/status")).status;
    kvRow(conn, "드라이브", (st.drive_title || "") + " (" + st.drive_id + ")");
    var vault = !st.vault_configured ? "설정 안 됨" : (st.vault_unlocked ? "잠금 해제됨" : "잠김" + (st.vault_hint ? " (" + st.vault_hint + ")" : ""));
    kvRow(conn, "금고", vault);
  } catch (e) { kvRow(conn, "드라이브", "조회 실패: " + e.message); }
  var tokRow = document.createElement("div"); tokRow.className = "kv";
  var kk = document.createElement("span"); kk.className = "k"; kk.textContent = "접속 토큰";
  var box = document.createElement("div"); box.id = "tokbox"; box.style.flex = "1";
  var inp = document.createElement("input"); inp.type = "text";
  inp.value = token || "(사용 안 함 — --no-auth로 실행 중)";
  inp.readOnly = true;
  var re = document.createElement("button"); re.textContent = "재발급";
  re.onclick = async function() {
    try {
      var j = await api("/api/settings/token", {method: "POST", headers: {"Content-Type": "application/json"}, body: "{}"});
      token = j.token;
      inp.value = token;
      try { history.replaceState({p: state.cwd}, "", pageURL(state.cwd)); } catch (e2) {}
      toast("토큰 재발급됨 — 북마크를 갱신하세요.");
    } catch (e) { toast("오류: " + e.message); }
  };
  box.appendChild(inp); box.appendChild(re);
  tokRow.appendChild(kk); tokRow.appendChild(box); conn.appendChild(tokRow);

  var disp = sect("표시"); m.appendChild(disp);
  function selRow(label, options, cur, cb) {
    var r = document.createElement("div"); r.className = "kv";
    var k2 = document.createElement("span"); k2.className = "k"; k2.textContent = label;
    var sel = document.createElement("select");
    options.forEach(function(o) {
      var op = document.createElement("option"); op.value = o[0]; op.textContent = o[1];
      if (o[0] === cur) op.selected = true;
      sel.appendChild(op);
    });
    sel.onchange = function() { cb(sel.value); };
    r.appendChild(k2); r.appendChild(sel); disp.appendChild(r);
  }
  selRow("기본 보기", [["details", "자세히"], ["icons", "큰 아이콘"]], state.view, function(v) { state.view = v; savePrefs(); renderFiles(); renderColHead(); syncMenus(); });
  selRow("정렬 기준", [["name", "이름"], ["date", "수정한 날짜"], ["type", "유형"], ["size", "크기"]], state.sortKey, function(v) { state.sortKey = v; savePrefs(); renderFiles(); renderColHead(); syncMenus(); });
  selRow("정렬 방향", [["1", "오름차순"], ["-1", "내림차순"]], String(state.sortDir), function(v) { state.sortDir = parseInt(v, 10); savePrefs(); renderFiles(); renderColHead(); });
  var drow = document.createElement("div"); drow.className = "kv";
  var dk = document.createElement("span"); dk.className = "k"; dk.textContent = "세부 정보 창";
  var lab = document.createElement("label"); lab.className = "chk";
  var dcb = document.createElement("input"); dcb.type = "checkbox"; dcb.checked = state.detailsPane;
  dcb.onchange = function() { state.detailsPane = dcb.checked; savePrefs(); renderDetails(); };
  lab.appendChild(dcb); lab.appendChild(document.createTextNode("기본적으로 표시"));
  drow.appendChild(dk); drow.appendChild(lab); disp.appendChild(drow);

  var up = sect("업로드"); m.appendChild(up);
  var erow = document.createElement("label"); erow.className = "chk";
  var ecb = document.createElement("input"); ecb.type = "checkbox"; ecb.checked = !!prefs.encryptUpload;
  ecb.onchange = function() { saveEncryptPref(ecb.checked); };
  erow.appendChild(ecb); erow.appendChild(document.createTextNode("업로드 시 암호화 (해제하면 평문으로 저장됨)"));
  up.appendChild(erow);

  var btns = document.createElement("div"); btns.className = "btns";
  var ok = document.createElement("button"); ok.textContent = "닫기"; ok.className = "primary"; ok.onclick = closeModal;
  btns.appendChild(ok); m.appendChild(btns);
  $("modalWrap").classList.add("open");
}

/* ---------- move-to dialog ---------- */
var moveTarget = "/";
async function openMoveTo() {
  var sel = selectedEntries();
  if (!sel.length) { toast("이동할 항목을 선택하세요."); return; }
  moveTarget = state.cwd;
  var m = $("modal"); m.textContent = "";
  var h = document.createElement("h3");
  h.textContent = sel.length === 1 ? "이동: " + sel[0].name : sel.length + "개 항목 이동";
  m.appendChild(h);
  var crumbs = document.createElement("div"); crumbs.id = "movecrumbs"; m.appendChild(crumbs);
  var list = document.createElement("div"); list.id = "movelist"; m.appendChild(list);
  async function renderMoveDir(dir) {
    moveTarget = dir;
    crumbs.textContent = "";
    var parts = dir.split("/").filter(Boolean);
    var home = document.createElement("a"); home.textContent = "⌂ /"; home.onclick = function() { renderMoveDir("/"); };
    crumbs.appendChild(home);
    var acc = "";
    parts.forEach(function(p) {
      acc += "/" + p;
      crumbs.appendChild(document.createTextNode(" › "));
      (function(t) { var a = document.createElement("a"); a.textContent = p; a.onclick = function() { renderMoveDir(t); }; crumbs.appendChild(a); })(acc);
    });
    list.textContent = "불러오는 중…";
    try {
      var j = await api("/api/list?path=" + encodeURIComponent(dir));
      list.textContent = "";
      var folders = (j.entries || []).filter(function(e) { return e.type === "folder"; });
      if (!folders.length) list.textContent = "(하위 폴더 없음 — 여기로 이동 가능)";
      folders.forEach(function(f) {
        var r = document.createElement("div"); r.className = "mrow";
        var ic = document.createElement("span"); ic.textContent = "📁";
        var nm = document.createElement("span"); nm.textContent = f.name;
        r.appendChild(ic); r.appendChild(nm);
        r.ondblclick = function() { renderMoveDir(f.path); };
        r.onclick = function() {
          var kids = list.querySelectorAll(".mrow");
          for (var i = 0; i < kids.length; i++) kids[i].classList.remove("cur");
          r.classList.add("cur");
        };
        list.appendChild(r);
      });
    } catch (e) { list.textContent = "오류: " + e.message; }
  }
  await renderMoveDir(moveTarget);
  var btns = document.createElement("div"); btns.className = "btns";
  var cancel = document.createElement("button"); cancel.textContent = "취소"; cancel.onclick = closeModal;
  var go = document.createElement("button"); go.textContent = "여기로 이동"; go.className = "primary";
  go.onclick = async function() {
    closeModal();
    await opMoveTo(moveTarget);
  };
  btns.appendChild(cancel); btns.appendChild(go); m.appendChild(btns);
  $("modalWrap").classList.add("open");
}
async function opMoveTo(dst) {
  var sel = selectedEntries();
  if (!sel.length) return;
  toast(dst + " (으)로 이동 중… (" + sel.length + "개)");
  for (var i = 0; i < sel.length; i++) {
    var name = baseName(sel[i].path);
    try {
      await api("/api/mv", {method: "POST", headers: {"Content-Type": "application/json"},
        body: JSON.stringify({src: sel[i].path, dst: (dst === "/" ? "" : dst) + "/" + name})});
    } catch (e) { toast("오류: " + e.message); load(state.cwd); return; }
  }
  load(state.cwd);
}
function modalProps(e) {
  var m = $("modal"); m.textContent = "";
  var h = document.createElement("h3"); h.textContent = e.name + " 속성"; m.appendChild(h);
  var box = document.createElement("div"); box.className = "props";
  [["유형", typeOf(e)], ["위치", e.path], ["크기", e.type === "folder" ? "(폴더)" : (fmtSize(e.size) + " (" + (e.size || 0) + " 바이트)")], ["수정한 날짜", fmtTime(e.upload_time)]].forEach(function(pair) {
    var pr = document.createElement("div"); pr.className = "pr";
    var k = document.createElement("span"); k.className = "k"; k.textContent = pair[0];
    var v = document.createElement("span"); v.className = "v"; v.textContent = pair[1];
    pr.appendChild(k); pr.appendChild(v); box.appendChild(pr);
  });
  m.appendChild(box);
  var btns = document.createElement("div"); btns.className = "btns";
  var ok = document.createElement("button"); ok.textContent = "닫기"; ok.className = "primary"; ok.onclick = closeModal;
  btns.appendChild(ok); m.appendChild(btns);
  $("modalWrap").classList.add("open");
}

/* ---------- operations ---------- */
async function opMkdir() {
  modalInput("새 폴더", "", "만들기", async function(name) {
    name = (name || "").trim();
    if (!name) return;
    try { await api("/api/mkdir", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({path: (state.cwd === "/" ? "" : state.cwd) + "/" + name})}); load(state.cwd); }
    catch (e) { toast("오류: " + e.message); }
  });
}
async function opRename() {
  var sel = selectedEntries();
  if (sel.length !== 1) { toast("이름을 바꿀 항목 하나를 선택하세요."); return; }
  var e = sel[0];
  modalInput("이름 바꾸기", e.name, "확인", async function(name) {
    name = (name || "").trim();
    if (!name || name === e.name) return;
    var dir = e.path.slice(0, e.path.lastIndexOf("/")) || "/";
    try { await api("/api/mv", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({src: e.path, dst: (dir === "/" ? "" : dir) + "/" + name})}); load(state.cwd); }
    catch (err) { toast("오류: " + err.message); }
  });
}
async function opDelete() {
  var sel = selectedEntries();
  if (!sel.length) { toast("삭제할 항목을 선택하세요."); return; }
  var msg = sel.length === 1 ? "다음을 삭제할까요?\n" + sel[0].path : sel.length + "개 항목을 삭제할까요?";
  modalConfirm("삭제", msg, async function() {
    toast("삭제 중…");
    for (var i = 0; i < sel.length; i++) {
      try { await api("/api/rm", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({path: sel[i].path, recursive: true})}); }
      catch (e) { toast("오류: " + e.message); load(state.cwd); return; }
    }
    load(state.cwd);
  });
}
function opCut() {
  var sel = selectedEntries();
  if (!sel.length) { toast("잘라낼 항목을 선택하세요."); return; }
  state.clip = {mode: "cut", items: sel.map(function(e) { return e.path; })};
  renderFiles(); renderStatus();
}
function opCopy() {
  var sel = selectedEntries();
  if (!sel.length) { toast("복사할 항목을 선택하세요."); return; }
  state.clip = {mode: "copy", items: sel.map(function(e) { return e.path; })};
  renderFiles(); renderStatus();
  toast(sel.length + "개 복사 대기 중 — 붙여넣을 폴더로 이동 후 붙여넣기");
}
function baseName(p) { var i = p.lastIndexOf("/"); return i < 0 ? p : p.slice(i + 1); }
function freeName(dir, name, taken) {
  if (taken.indexOf((dir === "/" ? "" : dir) + "/" + name) < 0) return name;
  var dot = name.lastIndexOf("."), stem = name, ext = "";
  if (dot > 0) { stem = name.slice(0, dot); ext = name.slice(dot); }
  for (var n = 2; ; n++) {
    var cand = stem + " - 복사본" + (n > 2 ? " (" + n + ")" : "") + ext;
    if (taken.indexOf((dir === "/" ? "" : dir) + "/" + cand) < 0) return cand;
  }
}
async function opPaste() {
  if (!state.clip || !state.clip.items.length) { toast("붙여넣을 항목이 없습니다."); return; }
  var items = state.clip.items.slice();
  var mode = state.clip.mode;
  var taken = state.entries.map(function(e) { return e.path; });
  toast((mode === "cut" ? "이동" : "복사") + " 중… (" + items.length + "개)");
  for (var i = 0; i < items.length; i++) {
    var src = items[i];
    var dst = (state.cwd === "/" ? "" : state.cwd) + "/" + freeName(state.cwd, baseName(src), taken);
    taken.push(dst);
    try {
      if (mode === "cut") await api("/api/mv", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({src: src, dst: dst})});
      else await api("/api/cp", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({src: src, dst: dst})});
    } catch (e) { toast("오류: " + e.message); load(state.cwd); return; }
  }
  state.clip = null;
  load(state.cwd);
}
function opDownload() {
  var sel = selectedEntries().filter(function(e) { return e.type === "file"; });
  if (!sel.length) { toast("다운로드할 파일을 선택하세요."); return; }
  (function next(i) {
    if (i >= sel.length) return;
    var a = document.createElement("a");
    a.href = withToken("/file?path=" + encodeURIComponent(sel[i].path) + "&download=1");
    a.download = sel[i].name;
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(function() { next(i + 1); }, 600);
  })(0);
}
function opProps() {
  var sel = selectedEntries();
  if (sel.length !== 1) { toast("속성을 볼 항목 하나를 선택하세요."); return; }
  modalProps(sel[0]);
}

/* ---------- context menu ---------- */
function hideCtx() { $("ctxmenu").style.display = "none"; }
function ctxItem(label, fn, disabled) {
  var b = document.createElement("button"); b.textContent = label; b.disabled = !!disabled;
  b.onclick = function() { hideCtx(); fn(); };
  return b;
}
function ctxSep() { var d = document.createElement("div"); d.className = "sep"; return d; }
function showCtx(x, y, kind) {
  var m = $("ctxmenu"); m.textContent = "";
  var sel = selectedEntries();
  if (kind === "items") {
    var one = sel.length === 1;
    m.appendChild(ctxItem("열기", function() { if (one) openEntry(sel[0]); else if (sel.length) openEntry(sel[0]); }));
    m.appendChild(ctxItem("다운로드", opDownload, !sel.some(function(e) { return e.type === "file"; })));
    m.appendChild(ctxSep());
    m.appendChild(ctxItem("✂ 잘라내기", opCut));
    m.appendChild(ctxItem("⧉ 복사", opCopy));
    m.appendChild(ctxItem("➦ 이동…", openMoveTo));
    m.appendChild(ctxSep());
    m.appendChild(ctxItem("✎ 이름 바꾸기", opRename, !one));
    m.appendChild(ctxItem("🗑 삭제", opDelete));
    m.appendChild(ctxItem("📄 속성", opProps, !one));
  } else {
    m.appendChild(ctxItem("📁 새로 만들기 → 폴더", opMkdir));
    m.appendChild(ctxItem("⬆ 업로드", function() { $("fileInput").click(); }));
    m.appendChild(ctxSep());
    m.appendChild(ctxItem("📋 붙여넣기", opPaste, !state.clip));
    m.appendChild(ctxItem("⟳ 새로 고침", function() { load(state.cwd, false); }));
  }
  m.style.display = "block";
  var r = m.getBoundingClientRect();
  m.style.left = Math.min(x, window.innerWidth - 220) + "px";
  m.style.top = Math.min(y, window.innerHeight - r.height - 10) + "px";
}

/* ---------- uploads (two stages) ---------- */
function uploadFiles(fileList) {
  var items = [];
  for (var i = 0; i < fileList.length; i++) items.push({file: fileList[i], dir: state.cwd, label: fileList[i].name});
  uploadQueue(items);
}
function uploadQueue(items) {
  if (!items.length) return;
  $("uploads").classList.add("open");
  var chain = Promise.resolve();
  items.forEach(function(item) { chain = chain.then(function() { return uploadOneFile(item); }); });
  chain.then(function() { toast("업로드 완료"); load(state.cwd); });
}
// Pure grouping for folder uploads: split webkitRelativePaths ("top/sub/f")
// into unique remote dirs (shallow first) plus per-file targets. Tested in
// node (see scripts/web-ui-folder.test.js).
function groupFolderUploads(fileList, base) {
  base = base === "/" ? "" : base;
  var seen = {}, items = [];
  for (var i = 0; i < fileList.length; i++) {
    var rel = fileList[i].webkitRelativePath || fileList[i].name;
    var slash = rel.lastIndexOf("/");
    var reldir = slash < 0 ? "" : rel.slice(0, slash);
    var remote = base + "/" + reldir;
    if (!seen[remote]) seen[remote] = true;
    items.push({file: fileList[i], dir: remote, label: rel});
  }
  var ordered = Object.keys(seen).sort(function(a, b) { return a.length - b.length; });
  return {dirs: ordered, items: items};
}
async function uploadFolderFiles(fileList) {
  if (!fileList.length) return;
  var grouped = groupFolderUploads(fileList, state.cwd);
  $("uploads").classList.add("open");
  toast("폴더 구조 생성 중… (" + grouped.dirs.length + "개)");
  for (var i = 0; i < grouped.dirs.length; i++) {
    try {
      await api("/api/mkdir", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({path: grouped.dirs[i]})});
    } catch (e) { toast("오류: " + e.message); return; }
  }
  uploadQueue(grouped.items);
}
function addUploadRow(name) {
  var box = document.createElement("div");
  box.className = "ufile";
  var title = document.createElement("div");
  title.className = "uname"; title.textContent = name; title.title = name;
  box.appendChild(title);
  var stages = {};
  [["s1", "① 브라우저→서버"], ["s2", "② 서버→텔레그램"]].forEach(function(pair) {
    var line = document.createElement("div");
    line.className = "stage";
    var lbl = document.createElement("span"); lbl.className = "lbl"; lbl.textContent = pair[1];
    var bar = document.createElement("progress"); bar.value = 0; bar.max = 100;
    var pct = document.createElement("span"); pct.className = "pct"; pct.textContent = "-";
    line.appendChild(lbl); line.appendChild(bar); line.appendChild(pct);
    box.appendChild(line);
    stages[pair[0]] = {bar: bar, pct: pct};
  });
  var list = $("ulist");
  list.insertBefore(box, list.firstChild);
  return stages;
}
function setStage(row, key, pct, text) {
  row[key].bar.value = pct;
  row[key].pct.textContent = text != null ? text : (Math.round(pct) + "%");
}
function uploadOneFile(item) {
  return new Promise(function(resolve) {
    var file = item.file || item;
    var dir = item.dir || state.cwd;
    var label = item.label || file.name;
    var row = addUploadRow(label);
    var tries = 0;
    (function attempt() {
      tries++;
      var fd = new FormData();
      fd.append("files", file, file.name);
      var xhr = new XMLHttpRequest();
      xhr.upload.onprogress = function(ev) {
        if (ev.lengthComputable) setStage(row, "s1", ev.loaded / ev.total * 100);
      };
      xhr.onload = function() {
        // 503 means the server-side Telegram queue is momentarily full when
        // dropping many files at once: the bytes are still here, retry once.
        if (xhr.status === 503 && tries < 2) {
          setStage(row, "s1", 0, "대기 중");
          setTimeout(attempt, 3000);
          return;
        }
        if (xhr.status !== 200) {
          var msg = "HTTP " + xhr.status;
          try { msg = JSON.parse(xhr.responseText).error || msg; } catch (e) {}
          setStage(row, "s1", 0, "실패"); setStage(row, "s2", 0, "실패");
          row.s1.pct.title = msg;
          resolve();
          return;
        }
        setStage(row, "s1", 100, "수신 완료");
        var job = JSON.parse(xhr.responseText).job;
        pollTelegramStage(row, job.id, resolve);
      };
      xhr.onerror = function() { setStage(row, "s1", 0, "실패"); setStage(row, "s2", 0, "-"); resolve(); };
      xhr.open("POST", withToken("/api/upload?path=" + encodeURIComponent(dir) + (prefs.encryptUpload === false ? "&encrypt=0" : "")));
      xhr.send(fd);
    })();
  });
}
function pollTelegramStage(row, jobId, resolve) {
  setStage(row, "s2", 0, "대기 중");
  function tick() {
    api("/api/upload/status?id=" + encodeURIComponent(jobId)).then(function(j) {
      var file = ((j.job || {}).files || [])[0];
      if (!file) { setStage(row, "s2", 0, "실패"); resolve(); return; }
      if (file.stage === "telegram" || file.stage === "queued") {
        setStage(row, "s2", file.progress || 0);
        setTimeout(tick, 700);
      } else if (file.stage === "done") {
        setStage(row, "s2", 100, "완료"); resolve();
      } else {
        setStage(row, "s2", file.progress || 0, "실패");
        row.s2.pct.title = file.error || "";
        resolve();
      }
    }).catch(function() { setStage(row, "s2", row.s2.bar.value, "재시도 중"); setTimeout(tick, 1500); });
  }
  tick();
}

/* ---------- search ---------- */
var searchTimer = null;
function doSearch(q) {
  clearTimeout(searchTimer);
  if (!q) { if (state.searching) load(state.cwd); return; }
  searchTimer = setTimeout(function() {
    api("/api/find?q=" + encodeURIComponent(q)).then(function(j) {
      state.searching = q;
      state.entries = (j.results || []).map(function(r) { return {type: r.type, name: r.name, path: r.path, size: r.size, upload_time: r.upload_time}; });
      state.sel = [];
      renderAll();
      toast("검색 결과 " + state.entries.length + "개");
    }).catch(function(e) { toast("오류: " + e.message); });
  }, 300);
}

/* ---------- menus / wiring ---------- */
function syncMenus() {
  var vw = $("menuView").querySelectorAll("button");
  for (var i = 0; i < vw.length; i++) vw[i].querySelector(".tick").textContent = vw[i].getAttribute("data-view") === state.view ? "✓" : "";
  var st = $("menuSort").querySelectorAll("button");
  for (var j = 0; j < st.length; j++) st[j].querySelector(".tick").textContent = st[j].getAttribute("data-sort") === state.sortKey ? "✓" : "";
}
function toggleMenu(id) {
  var m = $(id);
  var open = m.hidden;
  $("menuView").hidden = true; $("menuSort").hidden = true;
  m.hidden = !open;
}
function renderAll() {
  renderAddress(); renderSide(); renderColHead(); renderFiles(); renderStatus(); renderDetails(); syncMenus();
  $("btnBack").disabled = state.hidx <= 0;
  $("btnFwd").disabled = state.hidx >= state.hist.length - 1;
  $("btnUp").disabled = state.cwd === "/";
  $("btnPaste").disabled = !state.clip;
}
function wire() {
  $("btnBack").onclick = function() { goHist(-1); };
  $("btnFwd").onclick = function() { goHist(1); };
  window.onpopstate = syncHistToURL;
  $("btnUp").onclick = goUp;
  $("btnRefresh").onclick = function() { load(state.cwd, false); };
  $("search").oninput = function(ev) { doSearch(ev.target.value.trim()); };
  $("btnNew").onclick = opMkdir;
  $("btnCut").onclick = opCut;
  $("btnCopy").onclick = opCopy;
  $("btnPaste").onclick = opPaste;
  $("btnMoveTo").onclick = openMoveTo;
  $("btnSettings").onclick = openSettings;
  $("btnRename").onclick = opRename;
  $("btnDelete").onclick = opDelete;
  $("btnUpload").onclick = function() { $("fileInput").click(); };
  $("btnUploadDir").onclick = function() { $("dirInput").click(); };
  $("btnDownload").onclick = opDownload;
  $("btnProps").onclick = opProps;
  $("btnView").onclick = function() { toggleMenu("menuView"); };
  $("btnSort").onclick = function() { toggleMenu("menuSort"); };
  $("btnDetails").onclick = function() { state.detailsPane = !state.detailsPane; savePrefs(); renderDetails(); };
  var vw = $("menuView").querySelectorAll("button");
  for (var i = 0; i < vw.length; i++) vw[i].onclick = (function(b) { return function() { state.view = b.getAttribute("data-view"); savePrefs(); $("menuView").hidden = true; renderFiles(); renderColHead(); syncMenus(); }; })(vw[i]);
  var st = $("menuSort").querySelectorAll("button");
  for (var j = 0; j < st.length; j++) st[j].onclick = (function(b) { return function() { var k = b.getAttribute("data-sort"); if (state.sortKey === k) state.sortDir = -state.sortDir; else { state.sortKey = k; state.sortDir = 1; } savePrefs(); $("menuSort").hidden = true; renderFiles(); renderColHead(); syncMenus(); }; })(st[j]);
  $("closePv").onclick = function() { pvList = []; pvIdx = -1; $("stage").textContent = ""; $("preview").classList.remove("open"); };
  $("pvPrev").onclick = function() { pvStep(-1); };
  $("pvNext").onclick = function() { pvStep(1); };
  $("preview").onclick = function(ev) { if (ev.target.id === "preview") $("closePv").onclick(); };
  $("fileInput").onchange = function(ev) { uploadFiles(ev.target.files); ev.target.value = ""; };
  $("dirInput").onchange = function(ev) { uploadFolderFiles(ev.target.files); ev.target.value = ""; };
  $("files").oncontextmenu = function(ev) { if (ev.target.id === "files" || ev.target.className === "icons") { ev.preventDefault(); showCtx(ev.clientX, ev.clientY, "bg"); } };
  document.addEventListener("click", function(ev) {
    if (!ev.target.closest || (!ev.target.closest("#ctxmenu") && !ev.target.closest(".menuwrap"))) { hideCtx(); $("menuView").hidden = true; $("menuSort").hidden = true; }
  });
  document.addEventListener("keydown", function(ev) {
    var tag = (ev.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea") return;
    if ($("modalWrap").classList.contains("open") || $("preview").classList.contains("open")) {
      if (ev.key === "Escape") { closeModal(); $("closePv").onclick(); }
      else if (ev.key === "ArrowLeft" && $("preview").classList.contains("open")) pvStep(-1);
      else if (ev.key === "ArrowRight" && $("preview").classList.contains("open")) pvStep(1);
      return;
    }
    if (ev.key === "Enter") { var s = selectedEntries(); if (s.length === 1) openEntry(s[0]); }
    else if (ev.key === "Backspace") goUp();
    else if (ev.key === "Delete") opDelete();
    else if (ev.key === "F2") opRename();
    else if (ev.key === "Escape") { state.sel = []; renderFiles(); renderStatus(); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "a") { ev.preventDefault(); state.sel = state.entries.map(function(e) { return e.path; }); renderFiles(); renderStatus(); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "c") opCopy();
    else if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "x") opCut();
    else if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "v") opPaste();
  });
  var drop = $("drop"), depth = 0;
  window.addEventListener("dragenter", function(ev) { ev.preventDefault(); depth++; drop.classList.add("on"); });
  window.addEventListener("dragleave", function(ev) { ev.preventDefault(); if (--depth <= 0) { depth = 0; drop.classList.remove("on"); } });
  window.addEventListener("dragover", function(ev) { ev.preventDefault(); });
  window.addEventListener("drop", function(ev) { ev.preventDefault(); depth = 0; drop.classList.remove("on"); if (ev.dataTransfer.files.length) uploadFiles(ev.dataTransfer.files); });
}
wire();
loadPrefs();
renderAll();
(function() {
  var initial = pathFromURL();
  state.hist = [initial];
  state.hidx = 0;
  load(initial, false);
})();
</script>
</body>
</html>`
