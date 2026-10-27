"""Step 6: quota-driven selection.

  06_select.py --pre 65000   choose pages worth labelling, using tag dimensions only  -> out/preselect.txt
  06_select.py               choose the 10k dataset  -> selected table, out/gaps.json, out/coverage.json

Lazy greedy: each step adds the row that closes the most unmet quota (each bucket's shortfall weighted by
1/minimum, so rare buckets count more), while respecting the caps. Language targets are both a minimum and a cap.
Once every quota is met or unreachable, the rest is filled by language shortfall.
"""
import argparse
import collections
import hashlib
import json
import sys

import yaml

from common import OUT, PAGE_TYPES, TOPICS, bucket_keys, db
from rows import REGIONS, RENDER_MODES, TIERS, final_rows, pre_rows

ROOT = OUT.parent


def language_targets(q):
    t = {}
    for key, val in q["language_targets"].items():
        if isinstance(val, int):
            t[key] = val
        elif "each" in val:
            t.update({lang: val["each"] for lang in val["langs"]})
        else:
            t.update(val)
    return t


def build_quotas(q, total, pre):
    mins = {f"language:{k}": v for k, v in language_targets(q).items()}
    mins.update(q["minimums"])
    for dim, n in q["each_minimum"].items():
        if pre and dim in ("page_type", "topic"):
            continue
        values = {"page_type": PAGE_TYPES, "topic": TOPICS, "platform": q["platforms"]}[dim]
        mins.update({f"{dim}:{v}": n for v in values})
    if not pre:
        mins.update({f"tier_x_page_type:{t}|{p}": q["joint_minimum"]["tier_x_page_type"] for t in TIERS for p in PAGE_TYPES})
    mins.update({f"render_x_region:{m}|{r}": q["joint_minimum"]["render_x_region"] for m in RENDER_MODES for r in REGIONS})
    scale = total / q["total"]
    mins = {k: max(1, round(v * scale)) for k, v in mins.items()}
    lang_caps = {k: v for k, v in mins.items() if k.startswith("language:")}
    return mins, lang_caps


def jitter(url):
    return int(hashlib.sha1(url.encode()).hexdigest()[:6], 16) / 0xFFFFFF * 1e-6


def select(rows, q, total, pre):
    mins, lang_caps = build_quotas(q, total, pre)
    # countries: a small floor for every country we have, so the long tail of countries shows up
    for r in rows:
        if r.get("country"):
            mins.setdefault(f"country:{r['country']}", 3)
    caps = {}
    if not pre:
        for r in rows:
            for dim in ("page_type", "country"):
                if r.get(dim):
                    caps[f"{dim}:{r[dim]}"] = int(q["max_share"][dim] * total)
        caps["page_type:other"] = int(q["max_share"]["page_type:other"] * total)
    caps.update(lang_caps)
    max_dom = q["max_per_domain"]
    count, dom = collections.Counter(), collections.Counter()
    keys = [bucket_keys(r) for r in rows]
    for r, k in zip(rows, keys):
        r["_keys"] = k
        if f"language:{r['language']}" not in lang_caps:
            caps[f"language:{r['language']}"] = 0  # languages outside the plan are not selected

    def allowed(i):
        r = rows[i]
        return dom[r["domain"]] < max_dom and all(count[k] < caps[k] for k in rows[i]["_keys"] if k in caps)

    def gain(i):
        return sum((mins[k] - count[k]) / mins[k] for k in rows[i]["_keys"] if k in mins and count[k] < mins[k])

    chosen = []

    def take(i):
        chosen.append(i)
        dom[rows[i]["domain"]] += 1
        for k in rows[i]["_keys"]:
            count[k] += 1

    used = set()
    alive = list(range(len(rows)))
    while len(chosen) < total:
        scored = sorted(((gain(i) + jitter(rows[i]["url"]), i) for i in alive if allowed(i)), reverse=True)
        scored = [(g, i) for g, i in scored if g > 1e-5]
        if not scored:
            break
        alive = [i for _, i in scored]
        floor = scored[0][0] * 0.5
        for _, i in scored:
            if len(chosen) >= total:
                break
            if not allowed(i):
                continue
            if gain(i) >= max(floor, 1e-5):
                used.add(i)
                take(i)
        alive = [i for i in alive if i not in used]
    greedy_n = len(chosen)
    # fill: languages furthest below target first
    rest = sorted((i for i in range(len(rows)) if i not in used), key=lambda i: jitter(rows[i]["url"]))
    by_lang = collections.defaultdict(list)
    for i in rest:
        by_lang[rows[i]["language"]].append(i)
    while len(chosen) < total:
        open_langs = [l for l, lst in by_lang.items() if lst and count[f"language:{l}"] < caps.get(f"language:{l}", 0)]
        if not open_langs:
            break
        lang = min(open_langs, key=lambda l: count[f"language:{l}"] / caps[f"language:{l}"])
        i = by_lang[lang].pop()
        if allowed(i):
            used.add(i)
            take(i)
    # languages with no pages left (e.g. zu, ha) cannot reach their cap: hand their slots to the other languages,
    # lowest fill ratio first, so the set still reaches `total`
    if not pre:
        lang_left = {k for k in lang_caps}
        def allowed_over(i):
            r = rows[i]
            return dom[r["domain"]] < max_dom and all(count[k] < caps[k] for k in r["_keys"] if k in caps and k not in lang_left)
        for l in by_lang:
            by_lang[l] = [i for i in by_lang[l] if i not in used]
        while len(chosen) < total:
            open_langs = [l for l, lst in by_lang.items() if lst and f"language:{l}" in lang_caps]
            if not open_langs:
                break
            lang = min(open_langs, key=lambda l: count[f"language:{l}"] / caps[f"language:{l}"])
            i = by_lang[lang].pop()
            if allowed_over(i):
                used.add(i)
                take(i)
    print(f"selected {len(chosen)} ({greedy_n} by quota, {len(chosen) - greedy_n} fill) from {len(rows)}", file=sys.stderr)
    gaps = sorted(({"bucket": k, "have": count[k], "need": v} for k, v in mins.items() if count[k] < v),
                  key=lambda g: (g["have"] / g["need"], g["bucket"]))
    return [rows[i] for i in chosen], count, gaps


def assign_ids(chosen, dev_size):
    order = sorted(chosen, key=lambda r: (r["language"], r.get("page_type") or "", hashlib.sha1(r["url"].encode()).hexdigest()))
    step = max(1, round(len(order) / dev_size))
    split = {r["url"]: ("dev" if k % step == 0 else "test") for k, r in enumerate(order)}
    ids = sorted(chosen, key=lambda r: hashlib.sha1(("v1" + r["url"]).encode()).hexdigest())
    return [(r["url"], n, split[r["url"]]) for n, r in enumerate(ids, 1)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pre", type=int, default=0, help="preselect N pages for labelling (tag dimensions only)")
    ap.add_argument("--pre-all", action="store_true", help="with --pre: list every preselectable page, not only the quota pick")
    ap.add_argument("--require-stable", action="store_true", help="only rows that passed the 24h stability check")
    args = ap.parse_args()
    q = yaml.safe_load((ROOT / "quotas.yaml").read_text())
    conn = db()
    if args.pre:
        chosen, count, gaps = select(pre_rows(conn), q, args.pre, pre=True)
        if args.pre_all:
            chosen = pre_rows(conn)
        (OUT / "preselect.txt").write_text("\n".join(r["url"] for r in chosen) + "\n")
        (OUT / "gaps_pre.json").write_text(json.dumps(gaps, indent=1))
        print(f"wrote out/preselect.txt; {len(gaps)} buckets short (out/gaps_pre.json)")
        return
    rows = final_rows(conn, args.require_stable)
    chosen, count, gaps = select(rows, q, q["total"], pre=False)
    conn.execute("DELETE FROM selected")
    conn.executemany("INSERT INTO selected (url, id, split) VALUES (?,?,?)", assign_ids(chosen, q["dev_size"]))
    conn.commit()
    (OUT / "gaps.json").write_text(json.dumps(gaps, indent=1))
    coverage = {"rows": len(chosen), "countries": len({r["country"] for r in chosen if r.get("country")}),
                "domains": len({r["domain"] for r in chosen}), "buckets": dict(sorted(count.items()))}
    (OUT / "coverage.json").write_text(json.dumps(coverage, indent=1, ensure_ascii=False))
    print(f"{coverage['rows']} rows, {coverage['countries']} countries; {len(gaps)} buckets short")
    for x in gaps[:15]:
        print(f"  {x['bucket']}: {x['have']}/{x['need']}")

if __name__ == "__main__":
    main()
