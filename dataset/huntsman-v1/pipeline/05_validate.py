"""Step 5: validate labels.

  05_validate.py verify      second model (GPT Terra) checks page_type and the core/boilerplate choice; final render_mode
  05_validate.py stability   re-render pages >= 24h after the first fetch and require the truth snippet to still be there
"""
import argparse
import asyncio
import collections
import concurrent.futures as cf
import datetime as dt
import json
import sys
import threading
from pathlib import Path

from common import (PAGE_TYPES, VERIFY_MODEL, cache_path, copilot, db, parse_json_reply, read_gz_json, smart_tokenize)
from labeling import is_boiler, page_header, render_blocks

lock = threading.Lock()

VERIFY_INSTRUCTIONS = f"""You are checking another annotator's labels for a web-scraping benchmark. Each page is given as
numbered text blocks in reading order with DOM context in parentheses. Marked blocks:
  <<MAIN>> = the annotator's core-content range (start and end blocks are marked)
  <<TRUTH>> = blocks that the ~100-word ground-truth passage is cut from
  <<LIE>> = the block the boilerplate (non-core) snippet is cut from
For each page answer:
- page_type_ok: is the annotator's page_type a reasonable choice from {PAGE_TYPES}? If a different type is clearly
  better, set false and give it in better_page_type.
- truth_ok: is the TRUTH passage part of the page's core content (what a reader came for), not navigation, header,
  footer, sidebar, related links, ads, cookie text or comments?
- lie_ok: is the LIE block boilerplate that a good content extractor should drop? (true if there is no LIE block)
Reply with ONLY a JSON array: [{{"page": 1, "page_type_ok": true, "better_page_type": null, "truth_ok": true,
"lie_ok": true}}]
"""


def truth_block_ids(blocks, label):
    """Block ids the truth text was built from (same walk as labeling.build_snippets)."""
    ids, words = [], 0
    for b in blocks:
        if int(label["truth_block"]) <= b["i"] <= int(label["main_end"]) and not is_boiler(b["ctx"] or ""):
            ids.append(b["i"])
            words += len(b["text"].split()) or len(b["text"]) // 3
            if words >= 100:
                break
    return ids


def verify_batch(items):
    parts, datas = [], {}
    for k, (url, tags, label) in enumerate(items, 1):
        data = read_gz_json(cache_path(url, "json.gz"))
        datas[k] = (url, tags, label, data)
        marks = {int(label["main_start"]): "MAIN start", int(label["main_end"]): "MAIN end"}
        for i in truth_block_ids(data["blocks"], label):
            marks[i] = "TRUTH" + (" + MAIN start" if i == int(label["main_start"]) else "")
        if label.get("lie_block") is not None:
            marks[int(label["lie_block"])] = "LIE"
        parts.append(page_header(k, url, data, tags) + f"annotator page_type: {label['page_type']}\n"
                     + render_blocks(data["blocks"], marks=marks))
    reply = parse_json_reply(copilot(VERIFY_INSTRUCTIONS + "\n\n" + "\n\n".join(parts), VERIFY_MODEL, effort="low"))
    if isinstance(reply, dict):
        reply = [reply]
    by_page = {int(r.get("page", 0)): r for r in reply if isinstance(r, dict)}
    out = []
    for k, (url, tags, label, data) in datas.items():
        v = by_page.get(k)
        if not v:
            out.append((url, None, 0, "missing_in_reply"))
            continue
        v["render_mode"] = final_render_mode(tags, data, label)
        fails = [f for f in ("page_type_ok", "truth_ok", "lie_ok") if not v.get(f)]
        out.append((url, v, 0 if fails else 1, ",".join(fails) or None))
    return out


def final_render_mode(tags, data, label):
    """csr when the truth passage is missing from the raw (pre-JavaScript) HTML text."""
    if data["kind"] != "html":
        return "static"
    truth = collections.Counter(smart_tokenize(label["truth_text"]))
    raw = collections.Counter(smart_tokenize(data.get("raw_text") or ""))
    present = sum(min(c, raw[t]) for t, c in truth.items()) / max(sum(truth.values()), 1)
    if present < 0.5:
        return "csr"
    return "ssr" if tags.get("render_mode") == "ssr" or tags.get("platform") in (
        "nextjs", "nuxt", "gatsby_astro", "angular", "react_vue_spa") else "static"


def cmd_verify(args):
    conn = db()
    rows = conn.execute("SELECT l.url, l.label, t.tags FROM labels l JOIN tags t USING (url) WHERE l.ok = 1 "
                        "AND l.url NOT IN (SELECT url FROM verdicts" + (" WHERE reason IS NULL OR reason NOT LIKE 'call_failed%'" if args.retry_failed else "") + ")").fetchall()
    todo = [(r["url"], json.loads(r["tags"]), json.loads(r["label"])) for r in rows]
    if args.limit:
        todo = todo[:args.limit]
    batches = [todo[i:i + args.batch] for i in range(0, len(todo), args.batch)]
    print(f"verifying {len(todo)} pages in {len(batches)} calls", file=sys.stderr)
    done = [0, 0]

    def run(batch):
        try:
            res = verify_batch(batch)
        except Exception as e:
            res = []
            for item in batch:
                try:
                    res += verify_batch([item])
                except Exception as e2:
                    res.append((item[0], None, 0, f"call_failed: {str(e2)[:150]}"))
        with lock:
            for url, v, ok, reason in res:
                conn.execute("INSERT OR REPLACE INTO verdicts (url, verdict, ok, reason) VALUES (?,?,?,?)",
                             (url, json.dumps(v) if v else None, ok, reason))
                done[0] += 1
                done[1] += ok
            conn.commit()
            if done[0] % 60 < len(batch):
                print(f"verified {done[0]}/{len(todo)} ok {done[1]}", file=sys.stderr, flush=True)

    with cf.ThreadPoolExecutor(args.workers) as pool:
        list(pool.map(run, batches))
    print("verdicts:", [tuple(r) for r in conn.execute("SELECT coalesce(reason,'ok'), count(*) FROM verdicts GROUP BY 1 ORDER BY 2 DESC")])


def window_recall(content, truth):
    """Best recall of the truth tokens in any truth-sized window of the content (eval.py's metric)."""
    c, t = smart_tokenize(content), smart_tokenize(truth)
    if not c or not t:
        return 0.0
    win, tset, best = len(t), set(t), 0.0
    for i in range(0, max(len(c) - win + 1, 1)):
        best = max(best, len(set(c[i:i + win]) & tset) / len(tset))
        if best >= 0.999:
            break
    return best


async def _recheck(items, concurrency):
    import importlib
    fetch_mod = importlib.import_module("02_fetch")
    import httpx
    from playwright.async_api import async_playwright
    sem = asyncio.Semaphore(concurrency)
    results = {}
    async with async_playwright() as pw:
        browser = await pw.chromium.launch(args=["--disable-blink-features=AutomationControlled"])
        async with httpx.AsyncClient(http2=True, follow_redirects=True, headers={"User-Agent": fetch_mod.RAW_UA},
                                     verify=False) as client:
            f = fetch_mod.Fetcher(browser, client)

            async def one(url, truth, kind):
                async with sem:
                    text = ""
                    try:
                        if kind == "html":
                            rendered, _, _ = await asyncio.wait_for(f.render(url), 120)
                            text = " ".join(b["text"] for b in rendered["blocks"])
                        else:
                            _, data = await f.raw(url)
                            blocks, _ = await asyncio.to_thread(fetch_mod.doc_blocks, kind, data)
                            text = " ".join(b["text"] for b in blocks)
                    except Exception as e:
                        print(f"re-render failed {url}: {str(e)[:120]}", file=sys.stderr)
                    results[url] = await asyncio.to_thread(window_recall, text, truth)
                    if len(results) % 100 == 0:
                        print(f"rechecked {len(results)}/{len(items)}", file=sys.stderr, flush=True)

            await asyncio.gather(*(one(*it) for it in items))
        await browser.close()
    return results


def recheck(items, concurrency=8):
    """Re-render (url, truth_text, kind) items now; returns {url: window recall of the truth in the fresh text}."""
    return asyncio.run(_recheck(items, concurrency))


def cmd_stability(args):
    conn = db()
    cutoff = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=args.min_age_hours)).isoformat(timespec="seconds")
    scope = "SELECT url FROM selected" if args.selected_only else "SELECT url FROM verdicts WHERE ok = 1"
    rows = conn.execute(f"SELECT l.url, l.label, t.tags, f.fetched_at FROM labels l JOIN tags t USING (url) "
                        f"JOIN fetches f USING (url) WHERE l.url IN ({scope}) AND l.url NOT IN (SELECT url FROM stability)").fetchall()
    ready = [(r["url"], json.loads(r["label"])["truth_text"], json.loads(r["tags"])["kind"]) for r in rows
             if r["fetched_at"] <= cutoff]
    print(f"stability check for {len(ready)} pages ({len(rows) - len(ready)} not old enough yet)", file=sys.stderr)
    now = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
    for url, recall in recheck(ready, args.concurrency).items():
        conn.execute("INSERT OR REPLACE INTO stability (url, ok, recall, checked_at) VALUES (?,?,?,?)",
                     (url, int(recall >= 0.8), recall, now))
    conn.commit()
    print("stability:", [tuple(r) for r in conn.execute("SELECT ok, count(*) FROM stability GROUP BY 1")])


def main():
    sys.path.insert(0, str(Path(__file__).parent))
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("verify")
    p.add_argument("--batch", type=int, default=3)
    p.add_argument("--workers", type=int, default=8)
    p.add_argument("--limit", type=int, default=0)
    p.add_argument("--retry-failed", action="store_true")
    p = sub.add_parser("stability")
    p.add_argument("--min-age-hours", type=float, default=24)
    p.add_argument("--concurrency", type=int, default=8)
    p.add_argument("--selected-only", action="store_true")
    args = ap.parse_args()
    {"verify": cmd_verify, "stability": cmd_stability}[args.cmd](args)


if __name__ == "__main__":
    main()
