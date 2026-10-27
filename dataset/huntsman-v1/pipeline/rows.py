"""Dataset rows assembled from the state DB (shared by 06_select, 07_review and 08_export)."""
import json

from common import LANGS, REGION_OF, db

TIERS = sorted({t for _, t in LANGS.values()})
RENDER_MODES = ["static", "ssr", "csr"]
REGIONS = sorted(set(REGION_OF.values()))


def _base(url, tags):
    return {"url": url, "domain": tags.get("domain"), "language": tags.get("language"), "script": tags.get("script"),
            "direction": tags.get("direction"), "platform": tags.get("platform"), "source": tags.get("source"),
            "kind": tags.get("kind"), "render_mode": tags.get("render_mode"),
            "media": ";".join(tags.get("media") or []), "difficulty": ";".join(tags.get("difficulty") or [])}


def _region(row):
    row["region"] = REGION_OF.get(row.get("country") or "")
    return row


# Pages under this many main-content words pass labelling only ~28% of the time (vs ~77% above), so preselection
# skips them, except in long-tail languages where every page is worth a try.
MIN_PRE_WORDS = 150


def pre_rows(conn=None):
    """Rows from tags only (before labelling): no page_type/topic, country from the ccTLD."""
    conn = conn or db()
    rows = []
    for r in conn.execute("SELECT url, tags FROM tags"):
        tags = json.loads(r["tags"])
        if (tags.get("main_words") or 0) < MIN_PRE_WORDS and LANGS.get(tags.get("language"), (None, ""))[1] != "long_tail":
            continue
        row = _base(r["url"], tags)
        row["country"] = tags.get("country_tld")
        rows.append(_region(row))
    return rows


def final_rows(conn=None, require_stable=False):
    """Fully validated rows: label ok, verdict ok, not failed stability, not rejected in review (fixes applied)."""
    conn = conn or db()
    q = """SELECT t.url, t.tags, l.label, v.verdict, s.ok AS stable, rv.decision, rv.truth_text AS fix_truth,
                  rv.lie_text AS fix_lie, rv.reviewer
           FROM tags t JOIN labels l USING (url) JOIN verdicts v USING (url)
           LEFT JOIN stability s USING (url) LEFT JOIN reviews rv USING (url)
           WHERE l.ok = 1 AND v.ok = 1"""
    rows = []
    for r in conn.execute(q):
        if r["stable"] == 0 or (require_stable and r["stable"] is None) or r["decision"] == "reject":
            continue
        tags, label, verdict = json.loads(r["tags"]), json.loads(r["label"]), json.loads(r["verdict"])
        row = _base(r["url"], tags)
        country = label.get("country") if label.get("country") not in (None, "", "XX") else tags.get("country_tld")
        difficulty = [d for d in row["difficulty"].split(";") if d]
        difficulty += [w for w in label.get("walls") or [] if w in ("paywall", "login_wall") and w not in difficulty]
        row.update(page_type=label["page_type"], topic=label["topic"], country=(country or "").upper() or None,
                   render_mode=verdict.get("render_mode") or row["render_mode"], difficulty=";".join(difficulty),
                   truth_text=label["truth_text"], lie_text=label.get("lie_text") or "",
                   review="none", stable=r["stable"])
        if r["decision"] in ("ok", "fix"):
            row["review"] = "llm_review" if (r["reviewer"] or "").startswith("llm") else "human"
        if r["decision"] == "fix":
            row["truth_text"] = r["fix_truth"] or row["truth_text"]
            row["lie_text"] = r["fix_lie"] if r["fix_lie"] is not None else row["lie_text"]
        rows.append(_region(row))
    return rows
