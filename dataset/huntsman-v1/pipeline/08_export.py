"""Step 8: export.

  08_export.py [--prune-cache]

Writes data/huntsman-scrape-dataset-v1.csv (Firecrawl's v1 columns first, so its tooling still reads it), one parquet
file per `dev` / `test` split in data/, README.md (the Hugging Face dataset card), DATASHEET.md with per-dimension
counts, and copies the cached snapshot (blocks + raw text, screenshot, rendered HTML) of every selected row to
out/snapshots/. --prune-cache deletes cache files of rows that were not selected.

Publish from dataset/huntsman-v1 with `hf upload tulipfarmai/huntsman-scrape-dataset-v1 . . --repo-type dataset
--include README.md --include "data/*.csv" --include "data/*.parquet"`, then tag it with
`hf repos tag create tulipfarmai/huntsman-scrape-dataset-v1 v<version> --repo-type dataset`. Pass one pattern per
--include and both path arguments: otherwise the hf CLI reads a pattern as the destination path.
Snapshots are never published.
"""
import argparse
import collections
import csv
import datetime as dt
import shutil

from common import CACHE, LANGS, OUT, cache_path, db, snapshot_path
from rows import final_rows

ROOT = OUT.parent
DATA = ROOT / "data"
REPO = "https://github.com/tulipfarm/huntsman"
COLUMNS = ["id", "url", "truth_text", "lie_text", "error", "split", "language", "lang_tier", "script", "direction",
           "region", "country", "render_mode", "platform", "page_type", "topic", "media", "difficulty", "source",
           "review", "domain", "final_url", "http_status", "fetched_at"]
SUMMARY_DIMS = ["split", "lang_tier", "language", "direction", "script", "region", "render_mode", "platform",
                "page_type", "topic", "source", "review"]


def export_rows(conn):
    sel = {r["url"]: (r["id"], r["split"]) for r in conn.execute("SELECT url, id, split FROM selected")}
    fetch = {r["url"]: r for r in conn.execute("SELECT url, final_url, http_status, fetched_at FROM fetches")}
    out = []
    for r in final_rows(conn):
        if r["url"] not in sel:
            continue
        f = fetch[r["url"]]
        r.update(id=sel[r["url"]][0], split=sel[r["url"]][1], error="",
                 lang_tier=LANGS.get(r["language"], (None, "other_lang"))[1],
                 final_url=f["final_url"] or r["url"], http_status=f["http_status"], fetched_at=f["fetched_at"])
        out.append(r)
    missing = len(sel) - len(out)
    if missing:
        print(f"warning: {missing} selected rows no longer pass validation (re-run 06_select.py)")
    return sorted(out, key=lambda r: r["id"])


SOURCES = ["commoncrawl", "tranco", "search", "tulipfarm"]


def datasheet(rows, extra=()):
    """extra: Markdown lines added after the column list (e.g. a version note)."""
    lines = ["# huntsman-scrape-dataset-v1 datasheet", "",
             f"Generated {dt.date.today().isoformat()}. Design and method: SPEC.md.", "", *extra,
             "Columns: " + ", ".join(f"`{c}`" for c in COLUMNS) + ". `media` and `difficulty` are `;`-separated "
             "(empty difficulty = clean).", ""]
    return "\n".join(lines + dimension_tables(rows))


def dimension_tables(rows):
    lines = [f"{len(rows)} rows, "
             f"{len({r['domain'] for r in rows})} domains, {len({r['country'] for r in rows if r['country']})} countries, "
             f"{len({r['language'] for r in rows})} languages.", ""]
    for dim in SUMMARY_DIMS + ["media", "difficulty"]:
        c = collections.Counter()
        for r in rows:
            vals = (r.get(dim) or "").split(";") if dim in ("media", "difficulty") else [r.get(dim)]
            c.update(v or ("clean" if dim == "difficulty" else "unknown") for v in vals)
        lines += [f"## {dim}", "", "| value | rows | share |", "|---|---:|---:|"]
        lines += [f"| {v} | {n} | {n / len(rows):.1%} |" for v, n in c.most_common()]
        lines.append("")
    return lines


def write_splits(src, duckdb):
    for split in ("dev", "test"):
        duckdb.connect().execute(
            f"COPY (SELECT * FROM read_csv('{src}', header=true, all_varchar=true) WHERE split = '{split}' "
            f"ORDER BY CAST(id AS INTEGER)) TO '{DATA / split}.parquet' (FORMAT parquet, COMPRESSION zstd)")


def hf_card(rows, created=None, versions=()):
    """versions: (version, date, change) tuples, oldest first; the last one is the current version."""
    langs = sorted({r["language"] for r in rows if r["language"]})
    configs = ("- config_name: default\n  data_files:\n  - split: test\n    path: data/test.parquet\n"
               "  - split: dev\n    path: data/dev.parquet")
    created = created or dt.date.today().isoformat()
    sources = [s for s in SOURCES if any(r["source"] == s for r in rows)]
    version = f" Version {versions[-1][0]}, updated {versions[-1][1]}." if versions else ""
    lines = ["---", "license: cc-by-4.0", "language:"] + [f"- {l}" for l in langs] + [
        "multilinguality: multilingual", "size_categories:", "- 1K<n<10K", "task_categories:", "- text-retrieval",
        "pretty_name: Huntsman Scrape Dataset v1", "tags:", "- scrape", "- web-scraping", "- benchmark", "- boilerplate-removal", "configs:", configs, "---", "",
        "# Huntsman Scrape Dataset v1", "",
        "A benchmark for evaluating web scraping engines on content quality, balanced across language, script "
        "direction, rendering technology, page type, media format, difficulty, region and topic. It extends "
        "Firecrawl's [scrape-content-dataset-v1](https://huggingface.co/datasets/firecrawl/scrape-content-dataset-v1) and keeps "
        "its first five columns, so tooling for that dataset works unchanged.", "",
        f"Built by [Huntsman](https://github.com/TulipFarm/huntsman) ([source]({REPO})). The pipeline, quotas and design "
        f"spec are in [`dataset/huntsman-v1/`]({REPO}/tree/main/dataset/huntsman-v1).", "",
        "## Overview", "",
        f"{len(rows):,} rows, {len(langs)} languages, {len({r['country'] for r in rows if r['country']})} countries, "
        f"{len({r['domain'] for r in rows})} domains. Created {created}.{version}", "",
        "```python", "from datasets import load_dataset", "",
        'ds = load_dataset("tulipfarmai/huntsman-scrape-dataset-v1", split="test")', "```", "",
        "Use `dev` for tuning and report scores on `test`. `data/huntsman-scrape-dataset-v1.csv` holds both splits "
        f"and works with [`eval.py`]({REPO}/blob/main/dataset/eval.py).", "",
        "## Dataset Structure", "",
        "- `id`: identifier", "- `url`: full URL of the web page",
        "- `truth_text`: ~100-word core snippet (main content)",
        "- `lie_text`: ~10-word non-core snippet (navigation, footer, ads, cookie banner)",
        "- `error`: optional error message if page retrieval failed (always empty: failed pages are dropped)",
        "- `split`: `dev` or `test`",
        "- `language`, `lang_tier`, `script`, `direction` (`ltr` / `rtl` / `mixed`)",
        "- `region`, `country`", "- `render_mode` (`static` / `ssr` / `csr`), `platform`",
        "- `page_type`, `topic`", "- `media`, `difficulty`: `;`-separated lists (empty difficulty = clean)",
        "- `source` (" + " / ".join(f"`{s}`" for s in sources) + "), `review` (`none` / `llm_review` / `human`)",
        "- `domain`, `final_url`, `http_status`, `fetched_at`", "",
        "## How it was built", "",
        "Candidates come from the Common Crawl index, the Tranco top list and targeted search for rare buckets. Each "
        "page was rendered in a headless browser, tagged by rules, labelled by an LLM (snippets are checked to occur "
        "verbatim on the page) and verified by a second LLM. Most pages were re-rendered after 24 h to drop unstable "
        "pages. Right-to-left and long-tail-language rows get an extra LLM review, and a stratified 500-row sample "
        "was spot-checked by a third LLM. No human review.", "",
        *(["Source `tulipfarm` holds the pages of tulipfarm.ai, the site of Huntsman's publisher, from its sitemaps. "
           "It is exempt from the 3-URLs-per-domain limit, and its publisher location is set to the United States "
           "because the pages don't state one.", ""] if "tulipfarm" in sources else []),
        "## Limitations", "", f"**Created**: {created}", "",
        "- URLs may become unavailable, and content may be updated or removed",
        "- Snippets are short excerpts of third-party pages; full page content is not included",
        "- Scanned PDFs are excluded (no OCR)",
        "- Rare languages may have fewer rows than their quota", "",
        *(["## Versions", ""] + [f"- **{v}** ({d}): {c}" for v, d, c in versions] + [""] if versions else []),
        "## License", "",
        "The dataset (labels, metadata and selection) is released under "
        "[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). The `truth_text` and `lie_text` snippets are short "
        "excerpts of third-party web pages and remain the property of their owners; they are included for research "
        "and benchmarking. Please respect website terms of service and use appropriate rate limiting.", ""]
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--prune-cache", action="store_true")
    args = ap.parse_args()
    conn = db()
    rows = export_rows(conn)
    import duckdb
    DATA.mkdir(exist_ok=True)
    csv_path = DATA / "huntsman-scrape-dataset-v1.csv"
    with csv_path.open("w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=COLUMNS, extrasaction="ignore")
        w.writeheader()
        w.writerows(rows)
    write_splits(csv_path, duckdb)
    print(f"wrote {len(rows)} rows -> data/{csv_path.name}, data/dev.parquet, data/test.parquet")
    (ROOT / "DATASHEET.md").write_text(datasheet(rows))
    (ROOT / "README.md").write_text(hf_card(rows))
    copied = 0
    for r in rows:
        for ext in ("json.gz", "jpg", "html.gz"):
            src = cache_path(r["url"], ext)
            if src.exists():
                dst = snapshot_path(r["url"], ext)
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(src, dst)
                copied += 1
    print(f"wrote README.md, DATASHEET.md; {copied} snapshot files")
    if args.prune_cache:
        keep = {cache_path(r["url"], "x").stem.split(".")[0] for r in rows}
        removed = 0
        for p in CACHE.rglob("*"):
            if p.is_file() and p.name.split(".")[0] not in keep:
                p.unlink()
                removed += 1
        print(f"pruned {removed} cache files")


if __name__ == "__main__":
    main()
