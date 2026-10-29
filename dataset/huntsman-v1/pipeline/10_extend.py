"""Step 10: build a point release from the published CSV plus new validated rows, keeping the size fixed.

  10_extend.py [--sources tulipfarm,crash_scan] [--check-only]

Reads the v1 CSV (copied once to out/base_v1.csv, so re-runs start from the same rows) and the validated rows of the
given sources in out/state.db. New rows get ids after the current maximum and a ~10% dev split per source. For each
new row one v1 row of the same split is dropped, taken from the most over-filled quota buckets, never pushing a bucket
that v1 met below its minimum and never dropping a human-reviewed row. Writes the CSV, both parquet files, README.md
and DATASHEET.md, plus out/v1.1_added.csv and out/v1.1_dropped.csv.
"""
import argparse
import collections
import csv
import hashlib
import importlib
import shutil
import sys

import yaml

from common import DATASET_CSV, OUT, bucket_keys, db, domain_cap

export = importlib.import_module("08_export")
select = importlib.import_module("06_select")

ROOT = OUT.parent
BASE = OUT / "base_v1.csv"
VERSION, UPDATED, CREATED = "1.1", "2026-10-29", "2026-10-27"
# Publisher location the pages don't state, confirmed by the publisher: domain -> (country, region).
PUBLISHERS = {"tulipfarm.ai": ("US", "North America")}


def sha(s):
    return hashlib.sha1(s.encode()).hexdigest()


def load_base():
    if not BASE.exists():
        rows = list(csv.DictReader(DATASET_CSV.open(encoding="utf-8")))
        if any(r["source"] not in ("commoncrawl", "tranco", "search") for r in rows):
            sys.exit(f"{DATASET_CSV.name} is already extended and {BASE} is missing: restore the v1 CSV first")
        shutil.copy2(DATASET_CSV, BASE)
    return list(csv.DictReader(BASE.open(encoding="utf-8")))


def new_rows(conn, sources, base, targets):
    """Validated rows of `sources`, deduplicated against v1, in planned languages, within the per-domain limit."""
    seen = {r["url"] for r in base} | {r["final_url"] for r in base}
    dom = collections.Counter(r["domain"] for r in base)
    fetch = {r["url"]: r["final_url"] for r in conn.execute("SELECT url, final_url FROM fetches")}
    rows, skipped = [], collections.Counter()
    for r in sorted(export.final_rows(conn), key=lambda r: sha(r["url"])):
        if r["source"] not in sources:
            continue
        if r["url"] in seen or (fetch.get(r["url"]) or r["url"]) in seen:
            skipped[f"{r['source']}: duplicate of a v1 URL"] += 1
        elif r["language"] not in targets:
            skipped[f"{r['source']}: language {r['language']} not planned"] += 1
        elif dom[r["domain"]] >= domain_cap(r["domain"]):
            skipped[f"{r['source']}: domain limit"] += 1
        else:
            seen |= {r["url"], fetch.get(r["url"]) or r["url"]}
            dom[r["domain"]] += 1
            rows.append(r)
    return rows, skipped


def assign(conn, rows, first_id):
    """~10% dev per source (hash order), then ids in hash order. Stored in `selected` so export_rows can format them."""
    split = {}
    for src in sorted({r["source"] for r in rows}):
        group = sorted((r for r in rows if r["source"] == src), key=lambda r: sha(r["url"]))
        n_dev = round(len(group) / 10)
        split.update({r["url"]: "dev" if k < n_dev else "test" for k, r in enumerate(group)})
    ordered = sorted(rows, key=lambda r: sha("v" + VERSION + r["url"]))
    conn.execute("DELETE FROM selected")
    conn.executemany("INSERT INTO selected (url, id, split) VALUES (?,?,?)",
                     [(r["url"], first_id + k, split[r["url"]]) for k, r in enumerate(ordered)])
    conn.commit()
    out = export.export_rows(conn)
    for r in out:
        if r["domain"] in PUBLISHERS:
            r["country"], r["region"] = PUBLISHERS[r["domain"]]
    return out


def limits(q, rows):
    """Minimums (quota minimums, language targets, a floor of 3 per country) and caps (max_share, language targets)."""
    mins, lang_caps = select.build_quotas(q, q["total"], pre=False)
    countries = collections.Counter(r["country"] for r in rows if r["country"])
    mins.update({f"country:{c}": min(3, n) for c, n in countries.items()})
    caps = {f"{dim}:{r[dim]}": int(q["max_share"][dim] * q["total"]) for r in rows for dim in ("page_type", "country") if r[dim]}
    caps["page_type:other"] = int(q["max_share"]["page_type:other"] * q["total"])
    caps.update(lang_caps)
    return mins, caps


def choose_drops(base, added, q):
    """For each split, drop as many v1 rows as were added, greedily from the most over-filled buckets."""
    rows = base + added
    keys = {r["url"]: bucket_keys(r) for r in rows}
    mins, caps = limits(q, base)
    count = collections.Counter(k for r in rows for k in keys[r["url"]])
    # a bucket v1 left short of its minimum may not fall below its v1 count
    before = collections.Counter(k for r in base for k in keys[r["url"]])
    floor = {k: min(v, before[k]) for k, v in mins.items()}
    need = collections.Counter(r["split"] for r in added)
    dropped = []

    def droppable(r):
        return r["review"] != "human" and all(count[k] - 1 >= floor[k] for k in keys[r["url"]] if k in floor)

    def score(r):
        # rows over a cap go first; then rows whose tightest bucket has the most room, so scarce buckets keep their
        # slack for later drops; then rows from the most over-filled buckets overall
        ks = [k for k in keys[r["url"]] if k in mins]
        # measured against v1's own count, so buckets v1 already left a row over their cap are not drained
        over_cap = sum(max(0, count[k] - max(caps[k], before[k])) for k in keys[r["url"]] if k in caps)
        room = min((count[k] - floor[k] for k in ks if not k.startswith("language:")), default=10 ** 6)
        surplus = sum((count[k] - mins[k]) / mins[k] for k in ks)
        return over_cap, min(room, 50), surplus, sha(r["url"])

    pool = {split: [r for r in base if r["split"] == split] for split in need}
    for split, n in sorted(need.items()):
        for _ in range(n):
            cands = [r for r in pool[split] if droppable(r)]
            if not cands:
                sys.exit(f"no droppable {split} row left after {len(dropped)} drops")
            r = max(cands, key=score)
            pool[split].remove(r)
            dropped.append(r)
            count.subtract(keys[r["url"]])
    return dropped


def check(q, base, final):
    """Every bucket v1 met stays met, and nothing v1 had gets worse. Returns a list of problems."""
    mins, caps = limits(q, base)
    before = collections.Counter(k for r in base for k in bucket_keys(r))
    after = collections.Counter(k for r in final for k in bucket_keys(r))
    problems = [f"{k}: {after[k]} < min {v} (v1 had {before[k]})" for k, v in mins.items()
                if after[k] < min(v, before[k])]
    problems += [f"{k}: {after[k]} > cap {v} (v1 had {before[k]})" for k, v in caps.items()
                 if after[k] > max(v, before[k])]
    n_countries = lambda rows: len({r["country"] for r in rows if r["country"]})
    if n_countries(final) < min(q["min_countries"], n_countries(base)):
        problems.append(f"countries: {n_countries(final)} < {q['min_countries']}")
    dom = collections.Counter(r["domain"] for r in final)
    problems += [f"domain {d}: {n} rows" for d, n in dom.items() if n > domain_cap(d)]
    splits = collections.Counter(r["split"] for r in final)
    if len(final) != q["total"] or splits["dev"] != q["dev_size"]:
        problems.append(f"size {len(final)}, splits {dict(splits)}")
    return problems


def dropped_buckets(dropped, top=25):
    c = collections.Counter(k for r in dropped for k in bucket_keys(r)
                            if k.split(":")[0] in ("language", "region", "render_mode", "platform", "page_type", "topic",
                                                   "media", "difficulty"))
    return c.most_common(top)


def write_csv(path, rows, columns):
    with path.open("w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction="ignore")
        w.writeheader()
        w.writerows(rows)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sources", default="tulipfarm,crash_scan")
    ap.add_argument("--check-only", action="store_true", help="print the plan and the constraint check, write nothing")
    args = ap.parse_args()
    q = yaml.safe_load((ROOT / "quotas.yaml").read_text())
    conn = db()
    base = load_base()
    targets = select.language_targets(q)
    picked, skipped = new_rows(conn, set(args.sources.split(",")), base, targets)
    added = assign(conn, picked, max(int(r["id"]) for r in base) + 1)
    for r in added:
        r["id"] = str(r["id"])
    dropped = choose_drops(base, added, q)
    gone = {r["url"] for r in dropped}
    final = sorted([r for r in base if r["url"] not in gone] + added, key=lambda r: int(r["id"]))
    problems = check(q, base, final)

    by_source = collections.Counter((r["source"], r["split"]) for r in added)
    print("added:", dict(sorted(by_source.items())), "skipped:", dict(skipped))
    print("dropped:", dict(collections.Counter(r["split"] for r in dropped)), "by language:",
          dict(collections.Counter(r["language"] for r in dropped).most_common()))
    print("dropped per bucket:", dropped_buckets(dropped, 15))
    print("constraint check:", "ok" if not problems else f"{len(problems)} problems")
    for p in problems:
        print("  " + p)
    if args.check_only or problems:
        sys.exit(1 if problems else 0)

    cols = export.COLUMNS
    write_csv(DATASET_CSV, final, cols)
    import duckdb
    export.write_splits(DATASET_CSV, duckdb)
    write_csv(OUT / "v1.1_added.csv", added, cols)
    write_csv(OUT / "v1.1_dropped.csv", dropped, cols)

    n_src = collections.Counter(r["source"] for r in added)
    ids = sorted(int(r["id"]) for r in added)
    change = ("added " + " and ".join(f"{n} `{src}`" for src, n in sorted(n_src.items())) + " rows and removed "
              f"{len(dropped)} v1 rows from over-filled buckets, keeping 10,000 rows (dev 1,000, test 9,000). "
              "The 24 h stability check of the new rows is pending")
    versions = [("1.0", CREATED, "first release"), (VERSION, UPDATED, change)]
    note = [f"## Version {VERSION}", "",
            f"Updated {UPDATED}. Added {len(added)} rows (ids {ids[0]}–{ids[-1]}): "
            + ", ".join(f"{s} {n_src[s]} ({by_source[(s, 'dev')]} dev)" for s in sorted(n_src)) + ". "
            "Their 24 h stability check is pending (SPEC.md §5).", "",
            f"Removed {len(dropped)} v1 rows ({sum(r['split'] == 'dev' for r in dropped)} dev, "
            f"{sum(r['split'] == 'test' for r in dropped)} test), one per added row in the same split, from the most "
            "over-filled buckets. No bucket that v1 met fell below its minimum and no human-reviewed row was removed. "
            "Removed rows per bucket:", "", "| bucket | rows removed |", "|---|---:|"]
    note += [f"| {k} | {n} |" for k, n in dropped_buckets(dropped)]
    note += ["", "Removed ids: " + ", ".join(sorted((r["id"] for r in dropped), key=int)) + ".", ""]
    (ROOT / "DATASHEET.md").write_text(export.datasheet(final, extra=note))
    (ROOT / "README.md").write_text(export.hf_card(final, created=CREATED, versions=versions))
    print(f"wrote {len(final)} rows -> data/{DATASET_CSV.name}, data/dev.parquet, data/test.parquet, README.md, "
          "DATASHEET.md, out/v1.1_added.csv, out/v1.1_dropped.csv")


if __name__ == "__main__":
    main()
