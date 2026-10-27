#!/bin/sh
# One shared headless Chromium; spider-rs connects to it over CDP through CHROME_URL.
set -e
chromium --headless=new --no-sandbox --disable-dev-shm-usage --disable-gpu --no-first-run \
  --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 about:blank >/tmp/chromium.log 2>&1 &
until grep -q DevTools /tmp/chromium.log 2>/dev/null; do sleep 0.2; done
exec spider-scrape
