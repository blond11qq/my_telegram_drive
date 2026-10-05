// Unit tests for the pure folder-upload grouping in the web UI.
// No DOM needed: groupFolderUploads is extracted from the page script.
const fs = require("fs");
const assert = require("assert");

const pagePath = process.env.WEB_UI_HTML || "/tmp/tdrive-web-ui-page.html";
const html = fs.readFileSync(pagePath, "utf8");
const js = html.split("<script>")[1].split("</script>")[0];
const start = js.indexOf("function groupFolderUploads");
const end = js.indexOf("async function uploadFolderFiles");
if (start < 0 || end < 0) throw new Error("groupFolderUploads not found");
eval(js.slice(start, end));

const files = [
  { name: "a.txt", webkitRelativePath: "top/a.txt" },
  { name: "b.txt", webkitRelativePath: "top/sub/b.txt" },
  { name: "c.txt", webkitRelativePath: "top/sub/deep/c.txt" },
  { name: "d.txt", webkitRelativePath: "top/sub/b2.txt" },
];

let g = groupFolderUploads(files, "/docs");
assert.deepStrictEqual(g.dirs, ["/docs/top", "/docs/top/sub", "/docs/top/sub/deep"], "dirs shallow-first: " + JSON.stringify(g.dirs));
assert.strictEqual(g.items.length, 4);
assert.strictEqual(g.items[1].dir, "/docs/top/sub");
assert.strictEqual(g.items[2].label, "top/sub/deep/c.txt");
console.log("ok: folder grouping with base dir");

g = groupFolderUploads(files, "/");
assert.deepStrictEqual(g.dirs, ["/top", "/top/sub", "/top/sub/deep"], "root base: " + JSON.stringify(g.dirs));
console.log("ok: folder grouping at root");

const flat = [{ name: "x.txt" }];
g = groupFolderUploads(flat, "/docs");
assert.deepStrictEqual(g.dirs, ["/docs/"], "no relative path keeps target dir");
assert.strictEqual(g.items[0].label, "x.txt");
console.log("ok: plain files without relative path");

console.log("ALL FOLDER TESTS PASSED");
