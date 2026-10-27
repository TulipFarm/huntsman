"""Step 3: deterministic tags from the fetch cache (no LLM).

Language, script, direction, country (from ccTLD), platform, provisional render mode, media and difficulty flags.
render_mode is final only after labelling: 05_validate.py re-checks it against the truth snippet.
"""
import argparse
import collections
import concurrent.futures as cf
import json
import re
import sys
from urllib.parse import urlsplit

from labeling import is_boiler
from common import (RTL_LANGS, LANGS, cache_path, country_from_suffix, db, dominant_script, read_gz_json,
                    registered_domain, smart_tokenize)

FRAMEWORKS = ("next", "nuxt", "gatsby", "astro", "angular", "react", "vue")
PLATFORM_ORDER = [("shopify", "shopify"), ("wix", "wix"), ("squarespace", "squarespace"), ("webflow", "webflow"),
                  ("ghost", "ghost"), ("drupal", "drupal"), ("mediawiki", "mediawiki"), ("forum", "forum_software"),
                  ("docs", "docs_generator"), ("wordpress", "wordpress"), ("amp", "amp"), ("next", "nextjs"),
                  ("nuxt", "nuxt"), ("gatsby", "gatsby_astro"), ("astro", "gatsby_astro"), ("angular", "angular"),
                  ("react", "react_vue_spa"), ("vue", "react_vue_spa")]
UTF8 = {"utf-8", "utf8", "us-ascii", "ascii", None, ""}
LOCALE_TOKEN = re.compile(r"^([a-z]{2})([-_][a-z]{2})?$", re.I)


def detect_lang(text):
    from fast_langdetect import detect
    text = re.sub(r"\s+", " ", text)[:4000]
    if len(text) < 20:
        return None, 0.0
    best = detect(text)[0]
    lang = {"iw": "he", "nb": "no", "nn": "no", "zh-cn": "zh", "zh-tw": "zh"}.get(best["lang"], best["lang"])
    return lang, round(best["score"], 3)


def locale_tokens(url):
    parts = urlsplit(url)
    toks = {t.lower() for t in re.split(r"[/.?&=]", parts.path + "?" + parts.query) if LOCALE_TOKEN.match(t or "")}
    sub = parts.netloc.split(".")[0].lower()
    if LOCALE_TOKEN.match(sub):
        toks.add(sub)
    return toks


def tag_one(url, source, hint_lang):
    data = read_gz_json(cache_path(url, "json.gz"))
    blocks, kind, meta = data["blocks"], data["kind"], data["meta"]
    main_blocks = [b for b in blocks if not is_boiler(b["ctx"] or "")] or blocks
    main_text = " ".join(b["text"] for b in main_blocks)
    all_text = " ".join(b["text"] for b in blocks)
    lang, score = detect_lang(main_text)
    script, counts = dominant_script(main_text)
    letters = sum(counts.values()) or 1
    direction = "rtl" if (lang in RTL_LANGS or script in ("Arab", "Hebr", "Thaa")) else "ltr"
    if direction == "rtl" and counts.get("Latn", 0) / letters >= 0.2:
        direction = "mixed"
    final_url = meta.get("final_url") or url
    domain, suffix = registered_domain(final_url)
    words = len(smart_tokenize(main_text))
    media, difficulty = [], []
    raw = meta.get("raw", {})
    tags = {"language": lang, "lang_score": score, "hint_lang": hint_lang, "script": script, "direction": direction,
            "country_tld": country_from_suffix(suffix), "domain": domain, "source": source, "kind": kind,
            "main_words": words, "raw_ratio": None}
    if kind != "html":
        media.append("pdf" if kind == "pdf" else "office")
        tags.update(platform="none", render_mode="static")
        if words > 5000:
            media.append("long_form")
        if any(b["tag"] == "tr" for b in blocks) and sum(len(b["text"]) for b in blocks if b["tag"] == "tr") > 0.25 * len(all_text):
            media.append("tables")
    else:
        r = meta["render"]
        sig = r.get("sig", {})
        tags["html_lang"] = (r.get("htmlLang") or "").split("-")[0].lower() or None
        tags["platform"] = next((name for key, name in PLATFORM_ORDER if sig.get(key)), "custom")
        rendered = collections.Counter(smart_tokenize(main_text))
        rawc = collections.Counter(smart_tokenize(data.get("raw_text") or ""))
        overlap = sum(min(c, rawc[t]) for t, c in rendered.items())
        ratio = overlap / max(sum(rendered.values()), 1)
        tags["raw_ratio"] = round(ratio, 3)
        if ratio < 0.25:
            tags["render_mode"] = "csr"
        elif any(sig.get(f) for f in FRAMEWORKS):
            tags["render_mode"] = "ssr"
        else:
            tags["render_mode"] = "static"
        total = max(r.get("totalChars") or 0, 1)
        media.append("html")
        if (r.get("tableChars") or 0) >= 0.25 * total and r.get("tableCount"):
            media.append("tables")
        if (r.get("codeBlocks") or 0) >= 2 or (r.get("codeChars") or 0) >= 800:
            media.append("code")
        if (r.get("math") or 0) >= 3:
            media.append("math")
        if r.get("video"):
            media.append("video")
        if r.get("audio"):
            media.append("audio")
        if (r.get("images") or 0) >= 15 and words / max(r["images"], 1) < 60:
            media.append("image_heavy")
        if words > 5000:
            media.append("long_form")
        if (r.get("iframeChars") or 0) >= 1000 and r["iframeChars"] >= 0.4 * total:
            media.append("iframe_content")
        if r.get("cmpVisible") or r.get("fixedCookie") or r.get("tcf"):
            difficulty.append("cookie_banner")
        before, after = r.get("text_before_scroll") or 0, r.get("text_after_scroll") or 0
        if after >= 1.2 * before and after - before > 800:
            difficulty.append("lazy_load")
        if meta.get("raw_blocked"):
            difficulty.append("anti_bot")
        if r.get("jsonldFree") is False or r.get("paywallHint"):
            difficulty.append("paywall")
        if (r.get("shadowChars") or 0) >= 500 and r["shadowChars"] >= 0.2 * total:
            difficulty.append("shadow_dom")
        if (r.get("nodes") or 0) > 20000 or (r.get("htmlLength") or 0) > 5_000_000:
            difficulty.append("huge_dom")
        tags["login_hint"] = bool(r.get("loginHint"))
    if (raw.get("charset") or "").lower() not in UTF8:
        difficulty.append("non_utf8")
    hops = raw.get("hops") or []
    if len(hops) >= 3:
        difficulty.append("redirect_chain")
    if hops and (locale_tokens(final_url) - locale_tokens(url) or
                 registered_domain(url)[0] != domain and country_from_suffix(suffix)):
        difficulty.append("geo_redirect")
    tags["media"] = media
    tags["difficulty"] = difficulty
    return url, tags


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--retag", action="store_true")
    ap.add_argument("--workers", type=int, default=8)
    args = ap.parse_args()
    conn = db()
    q = ("SELECT f.url, c.source, c.hint_lang FROM fetches f LEFT JOIN candidates c USING (url) WHERE f.ok = 1"
         + ("" if args.retag else " AND f.url NOT IN (SELECT url FROM tags)"))
    rows = conn.execute(q).fetchall()
    print(f"tagging {len(rows)} pages", file=sys.stderr)
    with cf.ProcessPoolExecutor(args.workers) as pool:
        futs = [pool.submit(tag_one, r["url"], r["source"] or "search", r["hint_lang"]) for r in rows]
        for n, fut in enumerate(cf.as_completed(futs), 1):
            try:
                url, tags = fut.result()
            except Exception as e:
                print(f"tag failed: {e}", file=sys.stderr)
                continue
            conn.execute("INSERT OR REPLACE INTO tags (url, tags) VALUES (?, ?)", (url, json.dumps(tags, ensure_ascii=False)))
            if n % 500 == 0:
                conn.commit()
                print(f"tagged {n}/{len(rows)}", file=sys.stderr)
    conn.commit()
    langs = collections.Counter(json.loads(t)["language"] for (t,) in conn.execute("SELECT tags FROM tags"))
    known = sum(v for k, v in langs.items() if k in LANGS)
    print(f"tagged total {sum(langs.values())}; in target languages {known}; top: {langs.most_common(12)}")


if __name__ == "__main__":
    main()
