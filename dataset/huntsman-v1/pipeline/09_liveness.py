"""Step 9: liveness (run monthly).

  09_liveness.py [--threshold 0.8] [--concurrency 8]

Re-renders every row of the exported CSV and checks whether its truth_text is still on the page (window recall,
the eval's own metric). Writes out/liveness-<date>.csv (id, url, recall, alive) and lists the buckets that dead rows
belong to, so replacements can be drawn from the same buckets (06_select.py after marking them, then 08_export.py as
a new version).
"""
import argparse
import collections
import csv
import datetime as dt
import importlib
import sys
from pathlib import Path

from common import OUT, bucket_keys

sys.path.insert(0, str(Path(__file__).parent))
validate = importlib.import_module("05_validate")
CSV_PATH = OUT.parent / "data" / "huntsman-scrape-dataset-v1.csv"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--threshold", type=float, default=0.8)
    ap.add_argument("--concurrency", type=int, default=8)
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()
    rows = list(csv.DictReader(CSV_PATH.open(encoding="utf-8")))
    if args.limit:
        rows = rows[:args.limit]
    kind = lambda r: "pdf" if "pdf" in r["media"].split(";") else ("office" if "office" in r["media"].split(";") else "html")
    items = []
    for r in rows:
        k = kind(r)
        if k == "office":  # 02_fetch.doc_blocks needs the exact office type; take it from the URL
            ext = r["final_url"].lower().split("?")[0].rsplit(".", 1)[-1]
            k = {"docx": "docx", "pptx": "pptx", "xlsx": "xlsx"}.get(ext, "docx")
        items.append((r["url"], r["truth_text"], k))
    recall = validate.recheck(items, args.concurrency)
    out = OUT / f"liveness-{dt.date.today().isoformat()}.csv"
    dead = []
    with out.open("w", encoding="utf-8", newline="") as f:
        w = csv.writer(f)
        w.writerow(["id", "url", "recall", "alive"])
        for r in rows:
            rc = recall.get(r["url"], 0.0)
            alive = rc >= args.threshold
            w.writerow([r["id"], r["url"], f"{rc:.3f}", int(alive)])
            if not alive:
                dead.append(r)
    print(f"{len(rows) - len(dead)}/{len(rows)} alive ({len(dead)} dead) -> {out.name}")
    buckets = collections.Counter(k for r in dead for k in bucket_keys(r) if not k.startswith(("tier_x", "render_x")))
    for k, n in buckets.most_common(20):
        print(f"  {k}: {n} dead")


if __name__ == "__main__":
    main()
