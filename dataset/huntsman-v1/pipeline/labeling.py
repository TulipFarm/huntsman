"""Prompt rendering and snippet building shared by labelling, verification and review."""
import re

from common import PAGE_TYPES, TOPICS, UNSPACED_LANGS, normalize, smart_tokenize

BOILER_PART = re.compile(r"(nav|header|footer|aside|dialog|fixed|~(nav|menu|footer|header|sidebar|cookie|consent|gdpr|"
                         r"banner|breadcrumb|ads?|ad-|advert|promo|related|share|social|subscribe|newsletter|modal|popup|"
                         r"toolbar|masthead|widget|recommend|sponsor))$")


def is_boiler(ctx):
    """Boilerplate context. Only hints below the deepest main/article count: layout wrappers like
    'sidebar-layout' often sit above the real content, and an article's own <header> holds its title."""
    parts = (ctx or "").split(">")
    core = max((k for k, p in enumerate(parts) if p in ("main", "article")), default=-1)
    below = parts[core + 1:]
    return any(BOILER_PART.match(p) and not (core >= 0 and p == "header") for p in below)


TRUTH_WORDS = 100
TRUTH_CHARS_UNSPACED = 250
LIE_WORDS = 10
LIE_CHARS_UNSPACED = 25
BREAK_CHARS = "。！？，、；：.!?,;)）」』 \u3000"


def render_blocks(blocks, budget=9000, per_block=220, marks=None):
    """Blocks as numbered lines for a prompt. Long pages keep the start and the end and sample the middle."""
    marks = marks or {}
    lines = []
    for b in blocks:
        text = b["text"] if len(b["text"]) <= per_block else b["text"][:per_block] + "…"
        mark = f" <<{marks[b['i']]}>>" if b["i"] in marks else ""
        lines.append(f"[{b['i']}] ({b['ctx'] or '-'}){mark} {text}")
    total = sum(len(l) + 1 for l in lines)
    if total <= budget:
        return "\n".join(lines)
    head, tail, out, used = int(budget * 0.7), int(budget * 0.2), [], 0
    i = 0
    while i < len(lines) and used + len(lines[i]) < head:
        out.append(lines[i])
        used += len(lines[i]) + 1
        i += 1
    tail_lines, tused, j = [], 0, len(lines) - 1
    while j > i and tused + len(lines[j]) < tail:
        tail_lines.insert(0, lines[j])
        tused += len(lines[j]) + 1
        j -= 1
    marked = [lines[k] for k in range(i, j + 1) if "<<" in lines[k]]
    out.append(f"… [blocks {i}–{j} omitted] …")
    out += marked
    return "\n".join(out + tail_lines)


def page_header(key, url, data, tags):
    meta = data["meta"]
    title = (meta.get("render") or {}).get("title") or ""
    return (f"=== PAGE {key} ===\nurl: {url}\nfinal_url: {meta.get('final_url') or url}\nkind: {data['kind']}\n"
            f"title: {title[:200]}\ndetected_language: {tags.get('language')}\n")


LABEL_INSTRUCTIONS = f"""You label web pages for a web-scraping benchmark. Each page is given as numbered text blocks in
reading order, extracted from the rendered DOM. Each block shows its DOM context in parentheses (landmarks like
header/nav/main/article/footer/aside, "~hint" tokens from id/class names, "fixed" for fixed-position overlays,
"iframe", "pageN" for PDF pages).

For EACH page, decide:
- usable: false if the page is an error page, bot challenge, parked domain, empty shell, adult/gambling content, or has
  no real core content of at least ~100 words. Otherwise true.
- page_type: one of {PAGE_TYPES}. Use "login_error" only for pages whose core content is hidden behind a login prompt.
- topic: one of {TOPICS}.
- country: ISO 3166-1 alpha-2 code of the publisher's country (from domain, language, addresses, currency); "XX" if unclear.
- main_start, main_end: first and last block number of the page's core content (the part a reader came for: article
  body, product description, documentation text, forum posts, table data). Exclude navigation, headers, footers,
  sidebars, cookie banners, ads, related links, share buttons and comments.
- truth_block: the block where a ~100-word core passage starts. Choose body prose inside the core content, not the
  title, byline, date, breadcrumbs or a caption. The passage will run from this block through the following core blocks,
  so pick a block followed by enough core text.
- lie_block: one block of clear boilerplate (navigation menu, footer links, cookie banner, ad, newsletter prompt,
  related-links list) with at least 5 words if possible. null if the page has no boilerplate at all (e.g. plain PDFs).
- walls: list of any of ["paywall", "login_wall"] that visibly block part of the core content; [] otherwise.

Reply with ONLY a JSON array, one object per page, in this shape:
[{{"page": 1, "usable": true, "page_type": "...", "topic": "...", "country": "..", "main_start": 0, "main_end": 0,
   "truth_block": 0, "lie_block": 0, "walls": []}}]
"""


def _cut_spaced(words_iter_texts, limit):
    out = []
    for t in words_iter_texts:
        for w in t.split():
            out.append(w)
            if len(out) >= limit:
                return " ".join(out)
    return " ".join(out)


def _cut_unspaced(texts, limit, slack=80):
    acc = ""
    for t in texts:
        acc = (acc + " " + t).strip() if acc else t
        if len(acc.replace(" ", "")) >= limit:
            break
    if len(acc.replace(" ", "")) <= limit:
        return acc
    # cut at the first natural break after `limit` visible characters so the snippet ends on a token boundary
    visible = 0
    for idx, ch in enumerate(acc):
        if ch != " ":
            visible += 1
        if visible >= limit:
            for k in range(idx, min(idx + slack, len(acc))):
                if acc[k] in BREAK_CHARS:
                    return acc[:k + 1].strip()
            return acc
    return acc


def build_snippets(blocks, label, lang):
    """Deterministically cut truth/lie text from the chosen blocks. Returns (truth, lie, error)."""
    by_i = {b["i"]: b for b in blocks}
    try:
        ms, me, tb = int(label["main_start"]), int(label["main_end"]), int(label["truth_block"])
    except (KeyError, TypeError, ValueError):
        return None, None, "bad_block_ids"
    if not (ms <= tb <= me) or tb not in by_i:
        return None, None, "truth_outside_main"
    unspaced = lang in UNSPACED_LANGS
    cut = (lambda texts: _cut_unspaced(texts, TRUTH_CHARS_UNSPACED)) if unspaced else (lambda texts: _cut_spaced(texts, TRUTH_WORDS))
    truth = cut([by_i[i]["text"] for i in range(tb, me + 1) if i in by_i and not is_boiler(by_i[i]["ctx"] or "")])
    if not truth_long_enough(truth, lang):
        # page builders (Elementor, Divi...) wrap real content in "~widget"/"~share"/"~banner" classes: trust the
        # labeller's core range rather than the hint filter; verification still checks the passage
        truth = cut([by_i[i]["text"] for i in range(tb, me + 1) if i in by_i])
    lie = ""
    lb = label.get("lie_block")
    if lb is not None:
        try:
            lb = int(lb)
        except (TypeError, ValueError):
            return truth, None, "bad_lie_block"
        if ms <= lb <= me and not is_boiler((by_i.get(lb) or {}).get("ctx") or ""):
            return truth, None, "lie_inside_main"
        if lb not in by_i:
            return truth, None, "bad_lie_block"
        texts, k = [], lb
        while (k in by_i and (k == lb or k < ms or k > me or is_boiler(by_i[k]["ctx"] or ""))
               and len(" ".join(texts).split()) < 8 and len(texts) < 6):
            texts.append(by_i[k]["text"])
            k += 1
        lie = _cut_unspaced(texts, LIE_CHARS_UNSPACED, slack=20) if unspaced else _cut_spaced(texts, LIE_WORDS)
        if len(smart_tokenize(lie)) < (2 if unspaced else 3):
            lie = ""
    return truth, lie, None


def truth_long_enough(truth, lang):
    tok = smart_tokenize(truth or "")
    if lang in UNSPACED_LANGS:
        return len((truth or "").replace(" ", "")) >= 150 and len(tok) >= 5
    return len(tok) >= 60


# HTML tags, srcset lists, inline CSS or JSON-LD that leaked into page text: not readable core content
RAW_MARKUP = re.compile(r"<(img|svg|div|span|source|picture|a|p)\b|srcset|\{\s*(fill|stroke|display)\s*:|\"@(type|context)\"", re.I)


def check_snippets(blocks, label, truth, lie, lang):
    """Validation rules from SPEC §3 step 5. Returns an error string or None."""
    if not truth:
        return "empty_truth"
    if not truth_long_enough(truth, lang):
        return "truth_too_short"
    ms, me = int(label["main_start"]), int(label["main_end"])
    main_text = normalize(" ".join(b["text"] for b in blocks if ms <= b["i"] <= me and not is_boiler(b["ctx"] or "")))
    boiler_text = normalize(" ".join(b["text"] for b in blocks if not (ms <= b["i"] <= me) or is_boiler(b["ctx"] or "")))
    full = normalize(" ".join(b["text"] for b in blocks))
    if normalize(truth) not in full and normalize(truth) not in main_text:
        return "truth_not_in_page"
    if lie and normalize(lie) not in full:
        return "lie_not_in_page"
    if lie and normalize(lie) in main_text:
        return "lie_in_main"
    head = " ".join(normalize(truth).split()[:25]) if lang not in UNSPACED_LANGS else normalize(truth)[:60]
    # the labeller's whole core range counts here: hint-filtered text would reject page-builder layouts (see build_snippets)
    main_all = normalize(" ".join(b["text"] for b in blocks if ms <= b["i"] <= me))
    if head and head in boiler_text and head not in main_all:
        return "truth_in_boilerplate"
    return None
