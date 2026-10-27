"""Step 1: gather candidate URLs.

  01_candidates.py cc       sample Common Crawl's columnar index (by language, mime, ccTLD) plus Tranco top sites
  01_candidates.py search   fill under-quota buckets from out/gaps.json with local Firecrawl search
"""
import argparse
import concurrent.futures as cf
import csv
import gzip
import io
import json
import random
import shutil
import sys
import tarfile
import time
import urllib.request
import zipfile
from pathlib import Path

from common import (LANGS, OUT, QUOTAS, REGION_OF, FIRECRAWL_CSV, LABEL_MODEL, copilot, db, parse_json_reply,
                    registered_domain)

CRAWL = "CC-MAIN-2026-39"
CC_BASE = "https://data.commoncrawl.org/"
SEARCH_ENDPOINT = "http://127.0.0.1:3002/v1/search"
BLOCKLISTS = ["adult", "gambling", "malware", "phishing", "dating"]
OVERSAMPLE = 3.5
OFFICE_MIMES = ["application/vnd.openxmlformats-officedocument.wordprocessingml.document",
                "application/vnd.openxmlformats-officedocument.presentationml.presentation",
                "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"]


def language_targets():
    t = QUOTAS["language_targets"]
    out = {"en": t["en"], **t["rtl"]}
    for tier in ("major_ltr", "mid_ltr", "long_tail"):
        out.update({lang: t[tier]["each"] for lang in t[tier]["langs"]})
    return out


def blocked_domains():
    cache = OUT / "blocklist.txt"
    if not cache.exists():
        domains = set()
        for cat in BLOCKLISTS:
            url = f"https://dsi.ut-capitole.fr/blacklists/download/{cat}.tar.gz"
            print(f"downloading blocklist {cat}", file=sys.stderr)
            try:
                data = urllib.request.urlopen(url, timeout=300).read()
                with tarfile.open(fileobj=io.BytesIO(data)) as tar:
                    texts = [tar.extractfile(m).read() for m in tar.getmembers() if m.name.endswith("/domains")]
            except Exception:  # the university server is often down; GitHub mirror (adult is split in parts)
                mirror = f"https://raw.githubusercontent.com/olbat/ut1-blacklists/master/blacklists/{cat}/"
                names = ["domains.0", "domains.1", "domains.2"] if cat == "adult" else ["domains"]
                texts = [urllib.request.urlopen(mirror + n, timeout=300).read() for n in names]
            for text in texts:
                domains.update(l.strip().lstrip(".").lower() for l in text.decode("utf-8", "ignore").splitlines() if l.strip())
        cache.write_text("\n".join(sorted(domains)))
    return set(cache.read_text().split())


def tranco_domains(top=20000):
    cache = OUT / "tranco.csv"
    if not cache.exists():
        data = urllib.request.urlopen("https://tranco-list.eu/top-1m.csv.zip", timeout=300).read()
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            cache.write_bytes(z.read(z.namelist()[0]))
    rows = csv.reader(cache.open())
    return [d for _, d in (next(rows) for _ in range(top))]


def v1_urls():
    return {r["url"] for r in csv.DictReader(FIRECRAWL_CSV.open(encoding="utf-8"))}


def insert(conn, rows, blocked, exclude):
    added = 0
    for r in rows:
        url = r["url"]
        if url in exclude or len(url) > 400:
            continue
        domain, _ = registered_domain(url)
        if not domain or domain in blocked:
            continue
        cur = conn.execute("INSERT OR IGNORE INTO candidates (url, domain, source, hint_lang, hint_mime, hint_bucket) "
                           "VALUES (?,?,?,?,?,?)", (url, domain, r["source"], r.get("hint_lang"), r.get("hint_mime"),
                                                    r.get("hint_bucket")))
        added += cur.rowcount
    conn.commit()
    return added


def cmd_cc(args):
    import duckdb
    paths = gzip.decompress(urllib.request.urlopen(f"{CC_BASE}crawl-data/{CRAWL}/cc-index-table.paths.gz").read())
    files = [CC_BASE + p for p in paths.decode().split() if "subset=warc" in p]
    random.Random(42).shuffle(files)
    files = files[:args.files] if args.files else files
    con = duckdb.connect()
    con.execute("INSTALL httpfs; LOAD httpfs;")
    mimes = ",".join(f"'{m}'" for m in ['text/html', 'application/xhtml+xml', 'application/pdf'] + OFFICE_MIMES)
    raw_dir, part_dir = OUT / "cc_parts", OUT / "cc_red"
    part_dir.mkdir(parents=True, exist_ok=True)
    tmp = OUT / "duck_tmp"
    # Index files are sorted by SURT (reversed host), so each file covers only a slice of TLDs: scan all of them,
    # keeping at most 2 HTML URLs (and 20 documents) per domain per file so the parts stay small.
    reduce_sql = """SELECT url, domain, tld, lang, mime FROM (
        SELECT url, url_host_registered_domain AS domain, url_host_tld AS tld,
               split_part(content_languages, ',', 1) AS lang, content_mime_detected AS mime,
               row_number() OVER (PARTITION BY url_host_registered_domain, content_mime_detected LIKE 'text/%'
                                  ORDER BY hash(url)) AS r
        FROM read_parquet('{src}')
        WHERE fetch_status = 200 AND length(url) < 400 AND content_mime_detected IN ({mimes}))
      WHERE r <= CASE WHEN mime LIKE 'application/%' AND mime <> 'application/xhtml+xml' THEN 20 ELSE 2 END"""
    old_cols = """SELECT url, domain, tld, lang, mime FROM (
        SELECT *, row_number() OVER (PARTITION BY domain, mime LIKE 'text/%' ORDER BY hash(url)) AS r
        FROM read_parquet('{src}'))
      WHERE r <= CASE WHEN mime LIKE 'application/%' AND mime <> 'application/xhtml+xml' THEN 20 ELSE 2 END"""
    print(f"scanning {len(files)} index files from {CRAWL}", file=sys.stderr)
    done = [0]
    lock = __import__("threading").Lock()

    def scan(f):
        local = part_dir / (f.rsplit("/", 1)[-1])
        if local.exists():
            return
        raw = raw_dir / local.name  # earlier full downloads: reduce locally instead of fetching again
        c = duckdb.connect()
        c.execute(f"LOAD httpfs; SET http_retries=8; SET http_retry_wait_ms=2000; SET http_retry_backoff=2; "
                  f"SET threads=3; SET memory_limit='3GB'; SET temp_directory='{tmp}'; SET preserve_insertion_order=false;")
        sql = old_cols.format(src=raw) if raw.exists() else reduce_sql.format(src=f, mimes=mimes)
        for attempt in range(8):
            try:
                c.execute(f"COPY ({sql}) TO '{local}.tmp' (FORMAT parquet)")
                Path(f"{local}.tmp").rename(local)
                raw.unlink(missing_ok=True)
                break
            except Exception as e:  # CloudFront rate limits: back off and retry this file
                wait = 30 * (attempt + 1)
                print(f"  {local.name[:10]} attempt {attempt + 1} failed ({str(e)[:80]}), waiting {wait}s", file=sys.stderr)
                time.sleep(wait)
        with lock:
            done[0] += 1
            print(f"  file {done[0]}/{len(files)} done", file=sys.stderr, flush=True)

    with cf.ThreadPoolExecutor(args.parallel) as pool:
        list(pool.map(scan, files))
    missing = [f for f in files if not (part_dir / f.rsplit("/", 1)[-1]).exists()]
    if missing:
        sys.exit(f"{len(missing)} index files failed; re-run to resume")
    # ~250M rows: never materialise them. One URL per domain via a spilling hash aggregate, cached as parquet.
    con.execute(f"SET memory_limit='5GB'; SET temp_directory='{tmp}'; SET preserve_insertion_order=false;")
    con.execute(f"CREATE VIEW idx AS SELECT * FROM read_parquet('{part_dir}/*.parquet')")
    idx1_path = OUT / "cc_idx1.parquet"
    if not idx1_path.exists():
        con.execute(f"""COPY (SELECT domain, r.url, r.tld, r.lang, r.mime FROM (
                          SELECT domain, arg_min({{'url': url, 'tld': tld, 'lang': lang, 'mime': mime}}, hash(url)) r
                          FROM idx WHERE mime IN ('text/html', 'application/xhtml+xml') GROUP BY domain))
                        TO '{idx1_path}.tmp' (FORMAT parquet)""")
        Path(f"{idx1_path}.tmp").rename(idx1_path)
    con.execute(f"CREATE TABLE idx1 AS SELECT * FROM read_parquet('{idx1_path}')")
    print("index rows (1 per domain):", con.execute("SELECT count(*) FROM idx1").fetchone()[0], file=sys.stderr)

    conn, blocked, exclude = db(), blocked_domains(), v1_urls()
    rows = []
    # samples are deterministic prefixes (ORDER BY hash), so a bigger --scale re-run only adds new URLs
    grow = args.scale * args.oversample / OVERSAMPLE
    extra = json.loads(Path(args.lang_extra).read_text()) if args.lang_extra else None
    for lang, target in language_targets().items():
        iso3 = LANGS[lang][0]
        n = int(target * args.scale * args.oversample)
        if extra is not None:  # top-up: extend each language's prefix by its extra count, nothing else
            if lang not in extra:
                continue
            n += extra[lang]
        got = con.execute("SELECT url FROM idx1 WHERE lang = ? AND mime IN ('text/html','application/xhtml+xml') "
                          "ORDER BY hash(url || 'v1') LIMIT ?", [iso3, n]).fetchall()
        rows += [{"url": u, "source": "commoncrawl", "hint_lang": lang} for (u,) in got]
        print(f"  {lang}: {len(got)}/{n}", file=sys.stderr)
    if extra is not None:
        print("added", insert(conn, rows, blocked, exclude), "candidates")
        return
    for mime, n, tag in [("application/pdf", int(2500 * grow), "media:pdf")] + [(m, int(300 * grow), "media:office") for m in OFFICE_MIMES]:
        got = con.execute("SELECT url, lang FROM (SELECT *, row_number() OVER (PARTITION BY tld ORDER BY hash(url)) r "
                          "FROM (SELECT * FROM idx WHERE mime = ?)) WHERE r <= ? ORDER BY hash(url || 'v1') LIMIT ?", [mime, int(40 * grow), n]).fetchall()
        rows += [{"url": u, "source": "commoncrawl", "hint_mime": mime, "hint_lang": None, "hint_bucket": tag} for u, _ in got]
        print(f"  {tag} {mime.rsplit('.', 1)[-1]}: {len(got)}", file=sys.stderr)
    # under-represented regions: sample HTML pages per ccTLD whatever the language
    for region in ("Sub-Saharan Africa", "Oceania", "Latin America", "MENA", "Asia"):
        tlds = [c.lower() for c, r in REGION_OF.items() if r == region]
        got = con.execute(f"SELECT url FROM (SELECT *, row_number() OVER (PARTITION BY tld ORDER BY hash(url)) r FROM idx1 "
                          f"WHERE tld IN ({','.join('?' * len(tlds))}) AND mime = 'text/html') WHERE r <= ?", tlds + [int(60 * grow)]).fetchall()
        rows += [{"url": u, "source": "commoncrawl", "hint_bucket": f"region:{region}"} for (u,) in got]
        print(f"  region {region}: {len(got)}", file=sys.stderr)
    tranco = tranco_domains()
    con.execute("CREATE TABLE tranco(domain VARCHAR, rank INTEGER)")
    con.executemany("INSERT INTO tranco VALUES (?, ?)", [(d, i) for i, d in enumerate(tranco)])
    got = con.execute("SELECT i.url FROM (SELECT *, row_number() OVER (PARTITION BY domain ORDER BY hash(url)) r FROM "
                      "(SELECT * FROM idx WHERE mime = 'text/html' AND domain IN (SELECT domain FROM tranco))) i "
                      "JOIN tranco t USING (domain) WHERE i.r <= 2 ORDER BY t.rank LIMIT ?", [args.tranco]).fetchall()
    rows += [{"url": u, "source": "tranco"} for (u,) in got]
    print(f"  tranco: {len(got)}", file=sys.stderr)
    print("added", insert(conn, rows, blocked, exclude), "candidates")
    shutil.rmtree(tmp, ignore_errors=True)


BUCKET_HINTS = {
    "media:pdf": "PDF documents (add filetype:pdf to the query)",
    "media:office": "Word, PowerPoint or Excel files (filetype:docx / filetype:pptx / filetype:xlsx)",
    "media:tables": "pages whose main content is a data table (statistics, schedules, price lists, specs)",
    "media:code": "pages with code blocks (programming tutorials, API docs, snippets)",
    "media:math": "pages with mathematical formulas (lecture notes, math Q&A, physics explainers)",
    "media:video": "pages built around an embedded video player",
    "media:audio": "podcast episode pages or pages with an embedded audio player",
    "media:image_heavy": "photo galleries and image-heavy pages",
    "media:long_form": "very long articles (longreads, reports, full guides)",
    "media:iframe_content": "pages whose main content sits inside an iframe (embedded documents, widgets)",
    "difficulty:paywall": "news or magazine articles behind a metered or soft paywall",
    "difficulty:login_wall": "pages that show a sign-in prompt over the content",
    "difficulty:anti_bot": "popular sites protected by Cloudflare or other bot protection",
    "difficulty:lazy_load": "pages that load more content while scrolling (feeds, long listings)",
    "difficulty:cookie_banner": "European sites that show a cookie consent banner",
    "difficulty:shadow_dom": "sites built with web components (Lit, Stencil, Salesforce Lightning, Shoelace)",
    "difficulty:non_utf8": "older sites using legacy encodings (Shift_JIS, GBK, Big5, EUC-KR, Windows-1251/1256)",
    "difficulty:huge_dom": "huge pages (giant tables, full-text books, very long threads)",
    "render_mode:csr": "single-page apps that render content client-side (React/Vue/Angular)",
    "direction:mixed": "right-to-left language pages that mix in a lot of Latin text, code or numbers",
}


def describe(bucket):
    dim, _, value = bucket.partition(":")
    if bucket in BUCKET_HINTS:
        return BUCKET_HINTS[bucket], None
    if dim == "language":
        return f"any kind of web page written in language code '{value}'", value
    if dim in ("page_type", "topic"):
        return f"web pages whose {dim.replace('_', ' ')} is '{value}'", None
    if dim == "platform":
        return f"sites built with {value.replace('_', ' ')}", None
    if dim in ("region", "country"):
        return f"local websites from {value}", None
    if dim == "tier_x_page_type":
        tier, pt = value.split("|")
        return f"'{pt}' pages in a {tier.replace('_', ' ')} language (not English unless tier is english)", None
    if dim == "render_x_region":
        mode, region = value.split("|")
        return f"{mode} rendered pages (static/server-side/client-side) from sites in {region}", None
    return f"web pages in bucket {bucket}", None


def search(query, lang, limit):
    body = {"query": query, "limit": limit}
    if lang:
        body["lang"] = lang
    req = urllib.request.Request(SEARCH_ENDPOINT, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=90) as r:
            return [d["url"] for d in json.load(r).get("data", []) if d.get("url")]
    except Exception as e:  # one failed query should not stop gap filling
        print(f"search failed for {query!r}: {e}", file=sys.stderr)
        return []


def cmd_search(args):
    gaps = json.loads((OUT / args.gaps).read_text())  # [{"bucket", "have", "need"}] from 06_select.py
    conn, blocked, exclude = db(), blocked_domains(), v1_urls()
    todo = sorted(((g["bucket"], g["need"] - g["have"]) for g in gaps if g["need"] > g["have"]
                   and not g["bucket"].startswith(("source:", "lang_tier:", "script:"))), key=lambda t: -t[1])[:args.max_buckets]

    def plan(item):
        bucket, deficit = item
        what, lang = describe(bucket)
        n_queries = max(3, min(50, deficit // 3 + 2))
        prompt = (f"Write {n_queries} diverse web search queries that would find real, publicly accessible {what}.\n"
                  f"{'Write the queries in the language with code ' + lang + '.' if lang else 'Vary languages and countries where that makes sense.'}\n"
                  "Avoid adult, gambling and piracy content. Avoid Wikipedia. Each query should target a different site or "
                  "angle. Reply with only a JSON array of strings.")
        try:
            return bucket, lang, parse_json_reply(copilot(prompt, LABEL_MODEL, effort="low"))
        except Exception as e:
            print(f"query planning failed for {bucket}: {e}", file=sys.stderr)
            return bucket, lang, []

    with cf.ThreadPoolExecutor(args.workers) as pool:
        plans = list(pool.map(plan, todo))
    jobs = [(b, lang, q) for b, lang, qs in plans for q in qs if isinstance(q, str)]
    print(f"{len(jobs)} queries for {len(plans)} buckets", file=sys.stderr)
    with cf.ThreadPoolExecutor(4) as pool:
        results = pool.map(lambda j: (j, search(j[2], j[1], 10)), jobs)
        total = 0
        for (bucket, lang, _), urls in results:
            total += insert(conn, [{"url": u, "source": "search", "hint_lang": lang, "hint_bucket": bucket} for u in urls],
                            blocked, exclude)
    print("added", total, "candidates")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("cc")
    p.add_argument("--files", type=int, default=0, help="index parquet files to scan (default all ~300, 7M rows each)")
    p.add_argument("--parallel", type=int, default=3)
    p.add_argument("--tranco", type=int, default=4000)
    p.add_argument("--scale", type=float, default=1, help="multiply every sample size")
    p.add_argument("--lang-extra", help="JSON file {lang: extra candidates}: only extend these languages' samples")
    p.add_argument("--oversample", type=float, default=OVERSAMPLE, help="candidates per quota slot (before --scale)")
    p = sub.add_parser("search")
    p.add_argument("--max-buckets", type=int, default=200)
    p.add_argument("--gaps", default="gaps.json", help="gaps file in out/ (gaps.json or gaps_pre.json)")
    p.add_argument("--workers", type=int, default=6)
    args = ap.parse_args()
    {"cc": cmd_cc, "search": cmd_search}[args.cmd](args)


if __name__ == "__main__":
    main()
