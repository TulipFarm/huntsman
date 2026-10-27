"""Step 4: LLM labels (page_type, topic, country, core range, truth/lie block choice) with Claude Sonnet via Copilot.

  04_label.py [--batch 3] [--workers 8] [--limit N]

Labels pages listed in out/preselect.txt (from 06_select.py --pre) or, if that file is missing, every tagged page.
With --source search, labels every tagged page found by gap-fill search instead (--added-since limits that to one
round's candidates; the timestamp is UTC 'YYYY-MM-DD HH:MM:SS', like candidates.added_at).
"""
import argparse
import concurrent.futures as cf
import json
import sys
import threading

from common import (LABEL_MODEL, OUT, PAGE_TYPES, TOPICS, cache_path, copilot, db, parse_json_reply, read_gz_json)
from labeling import LABEL_INSTRUCTIONS, RAW_MARKUP, build_snippets, check_snippets, page_header, render_blocks

lock = threading.Lock()


def label_batch(items):
    """items: list of (url, tags). Returns list of (url, label_dict | None, ok, reason)."""
    datas, parts = {}, []
    for k, (url, tags) in enumerate(items, 1):
        data = read_gz_json(cache_path(url, "json.gz"))
        datas[k] = (url, tags, data)
        parts.append(page_header(k, url, data, tags) + render_blocks(data["blocks"]))
    prompt = LABEL_INSTRUCTIONS + "\n\n" + "\n\n".join(parts)
    reply = parse_json_reply(copilot(prompt, LABEL_MODEL, effort="low"))
    if isinstance(reply, dict):
        reply = [reply]
    by_page = {int(r.get("page", 0)): r for r in reply if isinstance(r, dict)}
    out = []
    for k, (url, tags, data) in datas.items():
        lab = by_page.get(k)
        out.append((url, None, 0, "missing_in_reply") if not lab else (url, *finish(lab, tags, data)))
    return out


def finish(lab, tags, data):
    """Turn one model label into (label, ok, reason): enum checks, then snippets cut from the chosen blocks."""
    if not lab.get("usable", True):
        return lab, 0, "unusable"
    if lab.get("page_type") not in PAGE_TYPES or lab.get("topic") not in TOPICS:
        return lab, 0, "bad_enum"
    lang = tags.get("language")
    truth, lie, err = build_snippets(data["blocks"], lab, lang)
    err = err or check_snippets(data["blocks"], lab, truth, lie, lang)
    if not err and "code" not in (tags.get("media") or []) and RAW_MARKUP.search(truth):
        err = "truth_raw_markup"  # markup leaked into page text; code pages may show markup on purpose
    lab.update(truth_text=truth, lie_text=lie)
    return lab, 0 if err else 1, err


SNIPPET_ERRORS = ("empty_truth", "truth_raw_markup", "truth_in_boilerplate", "truth_too_short", "truth_not_in_page", "lie_not_in_page", "lie_inside_main", "lie_in_main",
                  "truth_outside_main", "bad_lie_block")


def rebuild(conn):
    """Re-apply the snippet rules to stored labels that failed them (after a rules change). No model calls."""
    q = f"SELECT l.url, l.label, t.tags FROM labels l JOIN tags t USING (url) WHERE l.ok = 0 AND l.reason IN ({','.join('?' * len(SNIPPET_ERRORS))})"
    fixed = 0
    rows = conn.execute(q, SNIPPET_ERRORS).fetchall()
    for r in rows:
        lab, ok, reason = finish(json.loads(r["label"]), json.loads(r["tags"]), read_gz_json(cache_path(r["url"], "json.gz")))
        conn.execute("UPDATE labels SET label = ?, ok = ?, reason = ? WHERE url = ?", (json.dumps(lab, ensure_ascii=False), ok, reason, r["url"]))
        fixed += ok
    conn.commit()
    print(f"rebuilt {len(rows)} labels, {fixed} now pass")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--batch", type=int, default=3)
    ap.add_argument("--workers", type=int, default=8)
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--source", help="label all tagged pages from this candidate source (e.g. search)")
    ap.add_argument("--added-since", help="with --source: only candidates added at or after this UTC timestamp")
    ap.add_argument("--rebuild", action="store_true", help="re-apply snippet rules to stored failed labels, then exit")
    ap.add_argument("--relabel-failed", action="store_true", help="retry rows whose label call failed")
    args = ap.parse_args()
    conn = db()
    if args.rebuild:
        return rebuild(conn)
    pre = OUT / "preselect.txt"
    rows = conn.execute("SELECT url, tags FROM tags").fetchall()
    if args.source:  # gap-fill rounds: everything found by that source, regardless of the preselection
        rows = [r for r in rows if json.loads(r["tags"]).get("source") == args.source]
        if args.added_since:
            new = {u for (u,) in conn.execute("SELECT url FROM candidates WHERE added_at >= ?", [args.added_since])}
            rows = [r for r in rows if r["url"] in new]
    elif pre.exists():
        wanted = set(pre.read_text().split())
        rows = [r for r in rows if r["url"] in wanted]
    skip = {r[0] for r in conn.execute("SELECT url FROM labels" + (" WHERE reason NOT LIKE 'call_failed%'" if args.relabel_failed else ""))}
    todo = [(r["url"], json.loads(r["tags"])) for r in rows if r["url"] not in skip]
    if args.limit:
        todo = todo[:args.limit]
    batches = [todo[i:i + args.batch] for i in range(0, len(todo), args.batch)]
    print(f"labelling {len(todo)} pages in {len(batches)} calls", file=sys.stderr)
    done = [0, 0]

    def run(batch):
        try:
            res = label_batch(batch)
        except Exception as e:
            if len(batch) > 1:  # a bad reply for one page should not sink the others
                res = [r for item in batch for r in run_single(item)]
            else:
                res = [(batch[0][0], None, 0, f"call_failed: {str(e)[:150]}")]
        with lock:
            for url, lab, ok, reason in res:
                conn.execute("INSERT OR REPLACE INTO labels (url, label, ok, reason) VALUES (?,?,?,?)",
                             (url, json.dumps(lab, ensure_ascii=False) if lab else None, ok, reason))
                done[0] += 1
                done[1] += ok
            conn.commit()
            if done[0] % 60 < len(batch):
                print(f"labelled {done[0]}/{len(todo)} ok {done[1]}", file=sys.stderr, flush=True)

    def run_single(item):
        try:
            return label_batch([item])
        except Exception as e:
            return [(item[0], None, 0, f"call_failed: {str(e)[:150]}")]

    with cf.ThreadPoolExecutor(args.workers) as pool:
        list(pool.map(run, batches))
    reasons = conn.execute("SELECT coalesce(reason,'ok'), count(*) FROM labels GROUP BY 1 ORDER BY 2 DESC").fetchall()
    print("labels:", [tuple(r) for r in reasons])


if __name__ == "__main__":
    main()
