#!/bin/bash
# Regression test for the tdrive web gallery (prev/next across previewable
# siblings). Extracts the UI from the Go const, then runs it in jsdom.
# Usage: ./scripts/test-web-ui.sh   (needs node; jsdom is installed into a
# throwaway dir under /tmp on first run)
set -euo pipefail
cd "$(dirname "$0")/.."
python3 -c "
import re
src = open('cmd/tdrive/web_ui.go').read()
m = re.search(r'const webUIHTML = \`(.*)\`\$', src, re.S)
open('/tmp/tdrive-web-ui-page.html','w').write(m.group(1))
"
if [ ! -d /tmp/tdrive-web-ui-test/node_modules/jsdom ]; then
  mkdir -p /tmp/tdrive-web-ui-test
  (cd /tmp/tdrive-web-ui-test && npm init -y > /dev/null 2>&1 && npm i jsdom --no-audit --no-fund 2>&1 | tail -1)
fi
NODE_PATH=/tmp/tdrive-web-ui-test/node_modules WEB_UI_HTML=/tmp/tdrive-web-ui-page.html node scripts/web-ui-gallery.test.js
NODE_PATH=/tmp/tdrive-web-ui-test/node_modules WEB_UI_HTML=/tmp/tdrive-web-ui-page.html node scripts/web-ui-folder.test.js
