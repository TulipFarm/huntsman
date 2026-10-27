"""Step 7: review.

  07_review.py sample [--n 500]   stratified human-review sample (every bucket >= 5 rows) -> out/review/sample.txt
  07_review.py llm                Claude Opus reviews every selected RTL / long-tail / mixed-direction row
  07_review.py html               out/review/index.html: screenshot + snippets + ok/fix/reject, exports decisions JSON
  07_review.py apply FILE.json [--reviewer human]   load review decisions into the reviews table
  07_review.py report             fix+reject rate per bucket; flags buckets above 10%

Rejected rows drop out of rows.final_rows; re-run 06_select.py afterwards to backfill from the pool.
"""
import argparse
import base64
import collections
import concurrent.futures as cf
import html
import json
import sys
import threading

from common import (LANGS, OUT, REVIEW_MODEL, RTL_LANGS, bucket_keys, cache_path, copilot, db, normalize,
                    parse_json_reply, read_gz_json)
from labeling import page_header, render_blocks
from rows import final_rows

REVIEW_DIR = OUT.parent / "review"
lock = threading.Lock()


def selected_rows(conn):
    sel = {r["url"]: (r["id"], r["split"]) for r in conn.execute("SELECT url, id, split FROM selected")}
    rows = [r for r in final_rows(conn) if r["url"] in sel]
    for r in rows:
        r["id"], r["split"] = sel[r["url"]]
    return rows


def cmd_sample(args):
    conn = db()
    rows = sorted(selected_rows(conn), key=lambda r: r["id"])
    by_bucket = collections.defaultdict(list)
    for r in rows:
        for k in bucket_keys(r):
            if not k.startswith(("country:", "tier_x_page_type:", "render_x_region:")):
                by_bucket[k].append(r["url"])
    picked = []
    for k in sorted(by_bucket, key=lambda k: len(by_bucket[k])):  # rare buckets first
        have = sum(1 for u in by_bucket[k] if u in picked)
        picked += [u for u in by_bucket[k] if u not in picked][:max(0, 5 - have)]
    # top up uniformly (ids are a hash order, so this is a random sample)
    for r in rows:
        if len(picked) >= args.n:
            break
        if r["url"] not in picked:
            picked.append(r["url"])
    REVIEW_DIR.mkdir(exist_ok=True)
    (REVIEW_DIR / "sample.txt").write_text("\n".join(picked) + "\n")
    print(f"review sample: {len(picked)} rows -> review/sample.txt")


LLM_REVIEW = """You are a fluent reader of {lang_name} reviewing labels for a web-scraping benchmark. Each page is given
as numbered text blocks with DOM context. For each page you get the annotator's:
  truth_text: a ~100-word passage that must be part of the page's core content (what a reader came for)
  lie_text: a short snippet that must be boilerplate (navigation, footer, cookie text, related links...), or empty
Check, reading the text in its own language and direction:
- Is truth_text core content, coherent, and not navigation/boilerplate? Is the text correctly ordered (no reversed or
  garbled RTL/LTR runs)?
- Is lie_text boilerplate? (Empty is fine.)
- Is the given page_type right?
Reply with ONLY a JSON array: [{"page": 1, "decision": "ok" | "reject", "note": "short reason"}]
Use "reject" if any check fails.
"""


def cmd_llm(args):
    conn = db()
    done = {r[0] for r in conn.execute("SELECT url FROM reviews")}
    rows = [r for r in selected_rows(conn) if r["url"] not in done and
            (r["language"] in RTL_LANGS or r["direction"] != "ltr" or LANGS.get(r["language"], (0, ""))[1] == "long_tail")]
    by_lang = collections.defaultdict(list)
    for r in rows:
        by_lang[r["language"]].append(r)
    batches = [lst[i:i + args.batch] for lst in by_lang.values() for i in range(0, len(lst), args.batch)]
    print(f"LLM review of {len(rows)} rows in {len(batches)} calls", file=sys.stderr)
    stats = collections.Counter()

    def run(batch):
        parts = []
        for k, r in enumerate(batch, 1):
            data = read_gz_json(cache_path(r["url"], "json.gz"))
            parts.append(page_header(k, r["url"], data, r) + f"page_type: {r['page_type']}\n"
                         f"truth_text: {r['truth_text']}\nlie_text: {r['lie_text']}\n" + render_blocks(data["blocks"], budget=6000))
        prompt = LLM_REVIEW.replace("{lang_name}", f'the language with code "{batch[0]["language"]}"')
        try:
            reply = parse_json_reply(copilot(prompt + "\n\n" + "\n\n".join(parts), REVIEW_MODEL))
            reply = {int(x["page"]): x for x in (reply if isinstance(reply, list) else [reply])}
        except Exception as e:
            print(f"review call failed: {e}", file=sys.stderr)
            return
        with lock:
            for k, r in enumerate(batch, 1):
                v = reply.get(k)
                if not v or v.get("decision") not in ("ok", "reject"):
                    continue
                conn.execute("INSERT OR REPLACE INTO reviews (url, reviewer, decision, note) VALUES (?,?,?,?)",
                             (r["url"], f"llm:{REVIEW_MODEL}", v["decision"], (v.get("note") or "")[:300]))
                stats[v["decision"]] += 1
            conn.commit()

    with cf.ThreadPoolExecutor(args.workers) as pool:
        list(pool.map(run, batches))
    print("llm review:", dict(stats))


PAGE_HTML = """<!doctype html><meta charset="utf-8"><title>huntsman-scrape-dataset-v1 review</title>
<style>body{font:14px system-ui;margin:0}.row{display:grid;grid-template-columns:480px 1fr;gap:16px;padding:16px;
border-bottom:1px solid #ccc}img{width:480px;border:1px solid #ddd}textarea{width:100%;font:13px system-ui}
.t{background:#e8f7e8}.l{background:#fbe9e9}.meta{color:#555;font-size:12px}.done{opacity:.45}
#bar{position:sticky;top:0;background:#fff;padding:8px 16px;border-bottom:2px solid #333;z-index:1}</style>
<div id=bar><b>{n} rows</b> &middot; <span id=count>0</span> decided &middot; <button onclick=save()>Export decisions JSON</button>
 (then: 07_review.py apply decisions.json)</div>
{rows}
<script>
const KEY='hsd-v1-review';const d=JSON.parse(localStorage[KEY]||'{}');
function upd(){document.getElementById('count').textContent=Object.keys(d).length;
 for(const el of document.querySelectorAll('.row'))el.classList.toggle('done',!!d[el.dataset.url]);}
function dec(btn,decision){const row=btn.closest('.row');const u=row.dataset.url;
 d[u]={decision,truth_text:row.querySelector('.t').value,lie_text:row.querySelector('.l').value,
 note:row.querySelector('.n').value};localStorage[KEY]=JSON.stringify(d);upd();}
function save(){const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(d,null,1)]));
 a.download='decisions.json';a.click();}
upd();
</script>"""


def cmd_html(args):
    conn = db()
    wanted = (REVIEW_DIR / "sample.txt").read_text().split()
    rows = {r["url"]: r for r in selected_rows(conn)}
    out = []
    for url in wanted:
        r = rows.get(url)
        if not r:
            continue
        shot = cache_path(url, "jpg")
        img = (f'<img src="data:image/jpeg;base64,{base64.b64encode(shot.read_bytes()).decode()}">' if shot.exists()
               else "<i>no screenshot (document)</i>")
        dirattr = ' dir="auto"'
        out.append(f'<div class=row data-url="{html.escape(url)}"><div>{img}</div><div>'
                   f'<a href="{html.escape(url)}" target=_blank>{html.escape(url[:120])}</a>'
                   f'<div class=meta>#{r["id"]} &middot; {r["language"]} &middot; {r["page_type"]} &middot; {r["topic"]} &middot; '
                   f'{r.get("country")} &middot; {r["render_mode"]} &middot; {r["media"]} &middot; {r["difficulty"] or "clean"}</div>'
                   f'<p>truth (core content):</p><textarea class=t rows=6{dirattr}>{html.escape(r["truth_text"])}</textarea>'
                   f'<p>lie (boilerplate):</p><textarea class=l rows=2{dirattr}>{html.escape(r["lie_text"])}</textarea>'
                   f'<p><input class=n placeholder="note" style="width:60%"> '
                   f'<button onclick="dec(this,\'ok\')">ok</button> <button onclick="dec(this,\'fix\')">fix (save edits)</button> '
                   f'<button onclick="dec(this,\'reject\')">reject</button></p></div></div>')
    page = PAGE_HTML.replace("{n}", str(len(out))).replace("{rows}", "\n".join(out))
    (REVIEW_DIR / "index.html").write_text(page)
    print(f"wrote review/index.html with {len(out)} rows")


def cmd_apply(args):
    conn = db()
    decisions = json.loads(open(args.file).read())
    rows = {r["url"]: r for r in final_rows(conn)}
    n = 0
    for url, d in decisions.items():
        if d.get("decision") not in ("ok", "fix", "reject"):
            continue
        r = rows.get(url)
        decision = d["decision"]
        if decision == "ok" and r and "truth_text" in d and (normalize(d["truth_text"]) != normalize(r["truth_text"])
                                                              or normalize(d.get("lie_text", "")) != normalize(r["lie_text"])):
            decision = "fix"  # edited but clicked ok
        conn.execute("INSERT OR REPLACE INTO reviews (url, reviewer, decision, truth_text, lie_text, note) VALUES (?,?,?,?,?,?)",
                     (url, args.reviewer, decision, d.get("truth_text") if decision == "fix" else None,
                      d.get("lie_text") if decision == "fix" else None, d.get("note")))
        n += 1
    conn.commit()
    print(f"applied {n} decisions")


def cmd_report(args):
    conn = db()
    dec = {r["url"]: (r["reviewer"], r["decision"]) for r in conn.execute("SELECT url, reviewer, decision FROM reviews")}
    q = """SELECT t.url, t.tags, l.label, v.verdict FROM selected s JOIN tags t USING (url) JOIN labels l USING (url)
           JOIN verdicts v USING (url)"""
    per = collections.defaultdict(collections.Counter)
    for r in conn.execute(q):
        if r["url"] not in dec:
            continue
        tags, label = json.loads(r["tags"]), json.loads(r["label"])
        row = dict(tags, page_type=label["page_type"], topic=label["topic"], media=";".join(tags.get("media") or []),
                   difficulty=";".join(tags.get("difficulty") or []))
        reviewer, decision = dec[r["url"]]
        for k in ["all"] + [k for k in bucket_keys(row) if k.split(":")[0] in
                            ("language", "lang_tier", "direction", "page_type", "render_mode", "media", "difficulty")]:
            per[(reviewer, k)][decision] += 1
    flagged = []
    for (who, k), c in sorted(per.items()):
        n = sum(c.values())
        bad = (c["fix"] + c["reject"]) / n
        if k == "all" or (n >= 5 and bad > 0.10):
            line = f"{who:6} {k:40} n={n:4} ok={c['ok']:4} fix={c['fix']:3} reject={c['reject']:3} bad={bad:.0%}"
            print(line)
            if k != "all" and bad > 0.10:
                flagged.append(line)
    (REVIEW_DIR / "report.txt").write_text("\n".join(flagged) + "\n")
    print(f"{len(flagged)} buckets over 10% fix+reject -> review/report.txt")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("sample")
    p.add_argument("--n", type=int, default=500)
    p = sub.add_parser("llm")
    p.add_argument("--batch", type=int, default=4)
    p.add_argument("--workers", type=int, default=6)
    sub.add_parser("html")
    p = sub.add_parser("apply")
    p.add_argument("file")
    p.add_argument("--reviewer", default="human", help='e.g. "llm:gpt-6-astra" for a model spot check')
    sub.add_parser("report")
    args = ap.parse_args()
    {"sample": cmd_sample, "llm": cmd_llm, "html": cmd_html, "apply": cmd_apply, "report": cmd_report}[args.cmd](args)


if __name__ == "__main__":
    main()
