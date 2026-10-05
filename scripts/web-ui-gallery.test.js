const fs = require("fs");
const { JSDOM } = require("jsdom");

const entries = [
  { type: "file", name: "a.jpg", path: "/a.jpg", size: 10, upload_time: 1 },
  { type: "file", name: "b.mp4", path: "/b.mp4", size: 20, upload_time: 2 },
  { type: "file", name: "notes.txt", path: "/notes.txt", size: 5, upload_time: 3 },
  { type: "folder", name: "sub", path: "/sub" },
  { type: "file", name: "d.png", path: "/d.png", size: 8, upload_time: 4 },
  { type: "file", name: "run.exe", path: "/run.exe", size: 9, upload_time: 5 },
];

function fakeFetch(url) {
  if (url.includes("/api/status")) {
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ ok: true, status: { drive_id: 7, drive_title: "TDrive", current_path: "/", vault_configured: false, vault_unlocked: false } }) });
  }
  if (url.includes("/api/list")) {
    const m = url.match(/[?&]path=([^&]*)/);
    const p = m ? decodeURIComponent(m[1]) : "/";
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ ok: true, path: p, entries }) });
  }
  return Promise.reject(new Error("unexpected " + url));
}

const htmlPath = process.env.WEB_UI_HTML || "/tmp/pvtest/page.html";
const html = fs.readFileSync(htmlPath, "utf8");
const dom = new JSDOM(html, {
  url: "http://localhost/?token=t",
  runScripts: "dangerously",
  beforeParse(window) {
    window.fetch = fakeFetch;
    window.HTMLMediaElement.prototype.play = () => Promise.resolve();
  },
});
const { window } = dom;
const $ = (id) => window.document.getElementById(id);
const assert = (cond, msg) => { if (!cond) { console.error("FAIL:", msg); process.exit(1); } console.log("ok:", msg); };
const stageTag = () => ($("stage").firstChild || {}).tagName;

setTimeout(async () => {
  try {
    assert(window.location.search.includes("path="), "URL carries path, got " + window.location.search);
    await window.load("/sub");
    assert(window.location.search.includes("path=%2Fsub"), "URL updates on navigate, got " + window.location.search);
    await window.load("/");
    assert(window.document.querySelectorAll("#files .row").length === 6, "6 rows rendered");
    await window.load("/sub");
    window.history.back();
    await new Promise((r) => setTimeout(r, 100));
    assert(window.state.cwd === "/", "browser back returns to /, got " + window.state.cwd);
    assert(window.location.search.includes("path=%2F"), "URL back to /, got " + window.location.search);
    // details pane shows selection info only; open preview on b.mp4 (2nd previewable)
    window.preview(entries[1]);
    assert(stageTag() === "VIDEO", "b.mp4 shows VIDEO, got " + stageTag());
    assert($("pvcap").textContent.includes("b.mp4") && $("pvcap").textContent.includes("2 / 4"), "caption 'b.mp4 (2 / 4)', got " + $("pvcap").textContent);
    assert($("dl").href.includes("b.mp4") && $("dl").href.includes("download=1"), "download link follows item");
    // arrow right -> d.png (3/4)
    window.document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    assert(stageTag() === "IMG" && $("pvcap").textContent.includes("d.png"), "next shows d.png IMG, got " + stageTag() + " / " + $("pvcap").textContent);
    // next -> notes.txt (4/4), next wraps to a.jpg (1/4)
    window.pvStep(1);
    assert(stageTag() === "PRE", "notes.txt shows PRE, got " + stageTag());
    window.pvStep(1);
    assert(stageTag() === "IMG" && $("pvcap").textContent.includes("a.jpg") && $("pvcap").textContent.includes("1 / 4"), "wrap to a.jpg (1/4), got " + $("pvcap").textContent);
    // left from a.jpg wraps back to notes.txt (4/4)
    window.document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    assert($("pvcap").textContent.includes("notes.txt"), "left wraps to notes.txt, got " + $("pvcap").textContent);
    // close resets
    $("closePv").onclick();
    assert(!$("preview").classList.contains("open"), "preview closed");
    // non-previewable: single-item list, arrows disabled
    window.preview(entries[5]);
    assert($("stage").textContent.includes("지원하지 않는 형식"), "exe shows unsupported message");
    assert($("pvPrev").disabled && $("pvNext").disabled, "arrows disabled for single item");
    // settings dialog
    await window.openSettings();
    assert($("modalWrap").classList.contains("open"), "settings modal opens");
    assert($("modal").textContent.includes("접속 토큰"), "settings shows token section");
    assert($("modal").textContent.includes("TDrive"), "settings shows drive title");
    window.closeModal();
    // move-to dialog lists folders
    window.state.sel = ["/a.jpg"];
    await window.openMoveTo();
    assert($("modalWrap").classList.contains("open"), "move dialog opens");
    assert($("movelist").textContent.includes("sub"), "move dialog lists sub folder");
    window.closeModal();
    console.log("ALL GALLERY TESTS PASSED");
    process.exit(0);
  } catch (e) { console.error("FAIL:", e); process.exit(1); }
}, 300);
