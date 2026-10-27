#!/usr/bin/env python3
"""Run a scrape-content dataset (Firecrawl's v1 by default, or huntsman-scrape-dataset-v1 via --dataset) against a self-hosted scrape API and score it.

Scoring is copied from Firecrawl's scrape-evals (MIT, Copyright (c) 2025 Firecrawl); see NOTICE.
Usage: eval.py --engine huntsman --endpoint http://127.0.0.1:8080 --out results/dataset/huntsman
       eval.py ... --dataset dataset/huntsman-v1/data/huntsman-scrape-dataset-v1.csv --split test
Datasets with dimension columns (huntsman-scrape-dataset-v1) also get out/breakdown.json: the same metrics per dimension value.
"""
import argparse
import csv
import json
import os
import re
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

DATASET = Path(__file__).parent / "firecrawl-v1" / "scrape-content-dataset-v1.csv"


def smart_tokenize(text):
    return re.findall(r"\d+/\d+|[\w'-]+", (text or "").lower())


def strip_markdown(md):
    if not md:
        return ""
    text = re.sub(r"```[\s\S]*?```", " ", md)
    text = re.sub(r"`[^`]+`", " ", text)
    text = re.sub(r"!\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"^[#>\-\*\+\s]+", "", text, flags=re.MULTILINE)
    text = re.sub(r"[*_]{1,3}([^*_]+)[*_]{1,3}", r"\1", text)
    text = re.sub(r"\|", " ", text)
    return re.sub(r"\s+", " ", text).strip()


def window_scores(content_tokens, imp_tokens):
    if not content_tokens or not imp_tokens:
        return 0.0, 0.0, 0.0
    win = max(len(imp_tokens), 1)
    best_recall = best_precision = 0.0
    imp_set = set(imp_tokens)
    for i in range(0, max(len(content_tokens) - win + 1, 1)):
        wset = set(content_tokens[i:i + win])
        recall = len(wset & imp_set) / max(len(imp_set), 1)
        precision = len(wset & imp_set) / max(len(wset), 1)
        if recall > best_recall or (abs(recall - best_recall) < 1e-9 and precision > best_precision):
            best_recall, best_precision = recall, precision
    f1 = 2 * best_precision * best_recall / (best_precision + best_recall) if best_recall + best_precision else 0.0
    return best_recall, best_precision, f1


BLOCK_MARKERS = ["attention required", "cloudflare", "verify you are a human", "access denied", "bot detection",
                 "datadome", "akamai bot manager", "imperva", "sucuri website firewall"]


def score(task, result):
    content = result.get("content") or ""
    recall, precision, f1 = window_scores(smart_tokenize(strip_markdown(content)), smart_tokenize(task["truth_text"]))
    status = result.get("status_code")
    if not smart_tokenize(task["truth_text"]) and not smart_tokenize(task["lie_text"]):
        success = False
    else:
        success = bool(status is not None and 200 <= int(status) < 400 and not result.get("error") and content
                       and not any(m in content.lower() for m in BLOCK_MARKERS))
    return {"success": success, "recall": recall, "precision": precision, "f1": f1}


ENGINES = ["huntsman", "firecrawl", "crawl4ai", "spider"]


def request_for(endpoint, engine, url, timeout_ms):
    base = endpoint.rstrip("/")
    if engine == "crawl4ai":
        body = {"url": url, "f": "fit", "c": "0"}
        headers = {"Content-Type": "application/json"}
        token = os.environ.get("CRAWL4AI_API_TOKEN")
        if token:
            headers["Authorization"] = f"Bearer {token}"
        return urllib.request.Request(base + "/md", data=json.dumps(body).encode(), headers=headers)
    body = {"url": url, "formats": ["markdown"], "timeout": timeout_ms}
    if engine == "huntsman":
        body["maxAge"] = 0
    return urllib.request.Request(base + "/v1/scrape", data=json.dumps(body).encode(),
                                  headers={"Content-Type": "application/json"})


def scrape(endpoint, engine, url, timeout_ms):
    req = request_for(endpoint, engine, url, timeout_ms)
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout_ms / 1000 + 30) as resp:
            code, raw = resp.status, resp.read()
    except urllib.error.HTTPError as e:
        code, raw = e.code, e.read()
    except Exception as e:  # network errors and client timeouts count as failed scrapes
        return {"latency_ms": (time.monotonic() - start) * 1000, "status_code": None, "error": f"{type(e).__name__}: {e}", "content": None}
    latency = (time.monotonic() - start) * 1000
    try:
        payload = json.loads(raw)
    except ValueError:
        return {"latency_ms": latency, "status_code": code, "error": f"non-JSON response ({code})", "content": None}
    if engine == "crawl4ai":
        # /md reports no upstream status code; a successful render counts as 200.
        ok = code == 200 and payload.get("success", True) and payload.get("markdown")
        return {"latency_ms": latency, "status_code": 200 if ok else code,
                "error": None if ok else str(payload.get("detail") or payload.get("error") or f"HTTP {code}"),
                "content": payload.get("markdown")}
    data = payload.get("data") or {}
    meta = data.get("metadata") or {}
    error = payload.get("error") or meta.get("error")
    if code == 202:
        error = error or "timed out (job still pending)"
    status = meta.get("statusCode") or (code if payload.get("success") else None)
    return {"latency_ms": latency, "status_code": status, "error": error, "content": data.get("markdown")}


def percentile(values, p):
    if not values:
        return 0.0
    values = sorted(values)
    k = (len(values) - 1) * p
    lo, hi = int(k), min(int(k) + 1, len(values) - 1)
    return values[lo] + (values[hi] - values[lo]) * (k - lo)


BREAKDOWN_DIMS = ["language", "lang_tier", "direction", "script", "render_mode", "page_type", "topic", "region",
                  "platform", "source"]
MULTI_DIMS = ["media", "difficulty"]


def load_tasks(limit=0, dataset=None, split=None):
    tasks = list(csv.DictReader(Path(dataset or DATASET).open(encoding="utf-8")))
    if split:
        tasks = [t for t in tasks if t.get("split") == split]
    return tasks[:limit] if limit else tasks


def metrics(rows):
    return {"urls": len(rows),
            "success_rate": sum(r["success"] for r in rows) / len(rows),
            "coverage_recall_10pct": sum(r["recall"] >= 0.1 for r in rows) / len(rows),
            "avg_recall": statistics.fmean(r["recall"] for r in rows),
            "avg_precision": statistics.fmean(r["precision"] for r in rows),
            "avg_f1": statistics.fmean(r["f1"] for r in rows)}


def breakdown(tasks, rows):
    """Metrics per value of each dimension column present in the dataset (multi-valued columns are ';'-separated)."""
    groups = {}
    for t, r in zip(tasks, rows):
        for dim in BREAKDOWN_DIMS:
            if t.get(dim):
                groups.setdefault(dim, {}).setdefault(t[dim], []).append(r)
        for dim in MULTI_DIMS:
            for v in (t.get(dim) or "").split(";") if dim in t else []:
                groups.setdefault(dim, {}).setdefault(v or "clean", []).append(r)
    return {dim: {v: metrics(rs) for v, rs in sorted(vals.items(), key=lambda kv: -len(kv[1]))}
            for dim, vals in groups.items()}


def run_level(engine, endpoint, tasks, concurrency, timeout_ms, out, progress=True):
    """Scrape every task at a fixed concurrency, append rows to out/results.jsonl and return the summary."""
    out.mkdir(parents=True, exist_ok=True)
    raw_path, scored_path = out / "raw.jsonl", out / "results.jsonl"
    done = {}
    for path in (scored_path, raw_path):
        if path.exists():
            for line in path.open(encoding="utf-8"):
                row = json.loads(line)
                done[row["id"]] = row
    todo = [t for t in tasks if t["id"] not in done]
    lock = threading.Lock()
    finished = [0]

    def run(task):
        row = {"id": task["id"], "url": task["url"], **scrape(endpoint, engine, task["url"], timeout_ms)}
        with lock:
            with raw_path.open("a", encoding="utf-8") as f:
                f.write(json.dumps(row, ensure_ascii=False) + "\n")
            done[task["id"]] = row
            finished[0] += 1
            if progress and (finished[0] % 100 == 0 or finished[0] == len(todo)):
                print(f"{engine} c={concurrency}: {finished[0]}/{len(todo)}", file=sys.stderr, flush=True)

    wall_start = time.monotonic()
    with ThreadPoolExecutor(concurrency) as pool:
        list(pool.map(run, todo))
    wall = time.monotonic() - wall_start

    # Score after the timed run so scoring never competes with the engine for CPU.
    by_id = {t["id"]: t for t in tasks}
    for row in done.values():
        if "f1" not in row:
            row.update(score(by_id[row["id"]], row))
            row["content_chars"] = len(row.pop("content") or "")
    with scored_path.open("w", encoding="utf-8") as f:
        for t in tasks:
            f.write(json.dumps(done[t["id"]], ensure_ascii=False) + "\n")
    raw_path.unlink(missing_ok=True)

    rows = [done[t["id"]] for t in tasks]
    ok_latency = [r["latency_ms"] for r in rows if r["success"]]
    all_latency = [r["latency_ms"] for r in rows]
    summary = {
        "engine": engine,
        "concurrency": concurrency,
        **metrics(rows),
        "latency_ms_p50_all": percentile(all_latency, 0.5),
        "latency_ms_p95_all": percentile(all_latency, 0.95),
        "latency_ms_p50_success": percentile(ok_latency, 0.5),
        "latency_ms_p95_success": percentile(ok_latency, 0.95),
        "wall_seconds_this_run": wall if todo else None,
        "pages_per_second_this_run": len(todo) / wall if todo and wall else None,
    }
    (out / "summary.json").write_text(json.dumps(summary, indent=2))
    if any(dim in tasks[0] for dim in BREAKDOWN_DIMS):
        (out / "breakdown.json").write_text(json.dumps(breakdown(tasks, rows), indent=1, ensure_ascii=False))
    return summary


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--engine", required=True, choices=ENGINES)
    ap.add_argument("--endpoint", required=True)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--concurrency", type=int, default=10)
    ap.add_argument("--timeout-ms", type=int, default=30000)
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--dataset", type=Path, default=DATASET, help="dataset CSV (default: Firecrawl's v1)")
    ap.add_argument("--split", choices=["dev", "test"], help="only rows of this split (huntsman-scrape-dataset-v1)")
    args = ap.parse_args()
    tasks = load_tasks(args.limit, args.dataset, args.split)
    summary = run_level(args.engine, args.endpoint, tasks, args.concurrency, args.timeout_ms, args.out)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
