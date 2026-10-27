// Runs inside the page. Splits the rendered DOM into text blocks in reading order and collects page signals.
(() => {
  const SKIP = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "SVG", "CANVAS", "IFRAME", "OBJECT", "HEAD", "META", "LINK"]);
  const BLOCKISH = new Set(["DIV", "SECTION", "ARTICLE", "MAIN", "HEADER", "FOOTER", "NAV", "ASIDE", "UL", "OL", "DL", "LI",
    "TABLE", "THEAD", "TBODY", "TFOOT", "TR", "TD", "TH", "P", "H1", "H2", "H3", "H4", "H5", "H6", "PRE", "BLOCKQUOTE",
    "FIGURE", "FIGCAPTION", "FORM", "FIELDSET", "DETAILS", "SUMMARY", "DT", "DD", "CAPTION", "ADDRESS", "HR", "BODY",
    "CENTER", "MENU", "DIALOG", "HGROUP", "SEARCH", "LEGEND", "OPTION", "SELECT"]);
  const LANDMARK_TAGS = {HEADER: "header", NAV: "nav", FOOTER: "footer", ASIDE: "aside", MAIN: "main", ARTICLE: "article", FORM: "form", DIALOG: "dialog"};
  const LANDMARK_ROLES = {banner: "header", navigation: "nav", contentinfo: "footer", complementary: "aside", main: "main", dialog: "dialog", alertdialog: "dialog", search: "search"};
  const HINT_RE = /(nav|menu|footer|header|sidebar|cookie|consent|gdpr|banner|breadcrumb|\bads?\b|\bad-|advert|promo|related|share|social|comment|subscribe|newsletter|modal|popup|paywall|login|signin|toolbar|masthead|widget|recommend|sponsor|teaser|tags?\b|author|byline|caption|toc)/i;
  const blocks = [];
  let shadowChars = 0, iframeChars = 0, totalChars = 0;
  const MAX_BLOCKS = 4000;

  function hasBlockDescendant(el) {
    for (const c of el.children) {
      if (SKIP.has(c.tagName.toUpperCase())) continue;
      if (BLOCKISH.has(c.tagName.toUpperCase()) || c.shadowRoot || hasBlockDescendant(c)) return true;
    }
    return false;
  }

  function ctxFor(el, ctx) {
    const tag = el.tagName ? el.tagName.toUpperCase() : "";
    const out = ctx.slice();
    if (LANDMARK_TAGS[tag]) out.push(LANDMARK_TAGS[tag]);
    const role = el.getAttribute && el.getAttribute("role");
    if (role && LANDMARK_ROLES[role]) out.push(LANDMARK_ROLES[role]);
    const hint = ((el.id || "") + " " + (typeof el.className === "string" ? el.className : "")).slice(0, 200);
    const m = hint.match(HINT_RE);
    if (m && out[out.length - 1] !== "~" + m[1].toLowerCase()) out.push("~" + m[1].toLowerCase());
    if (el.getAttribute && el.getAttribute("aria-hidden") === "true") out.push("aria-hidden");
    if (out.length !== ctx.length) {
      try {
        const pos = getComputedStyle(el).position;
        if (pos === "fixed" || pos === "sticky") out.push("fixed");
      } catch (e) {}
    }
    return out.slice(-5);
  }

  function emit(text, tag, ctx, flags) {
    text = (text || "").replace(/\s+/g, " ").trim();
    if (!text || blocks.length >= MAX_BLOCKS) return;
    if (text.length > 5000) text = text.slice(0, 5000);
    totalChars += text.length;
    if (flags.shadow) shadowChars += text.length;
    if (flags.iframe) iframeChars += text.length;
    blocks.push({i: blocks.length, tag: tag.toLowerCase(), ctx: ctx.join(">"), text, shadow: !!flags.shadow, iframe: !!flags.iframe});
  }

  function visible(el) {
    try {
      if (el.checkVisibility && !el.checkVisibility({checkOpacity: false, checkVisibilityCSS: true})) {
        return getComputedStyle(el).display === "contents";
      }
    } catch (e) {}
    return true;
  }

  function walk(node, ctx, flags) {
    let run = [];
    const flush = () => { if (run.length) { emit(run.join(" "), "text", ctx, flags); run = []; } };
    const kids = node.shadowRoot ? [...node.shadowRoot.childNodes, ...node.childNodes] : node.childNodes;
    for (const c of kids) {
      if (blocks.length >= MAX_BLOCKS) return;
      if (c.nodeType === 3) { if (c.textContent.trim()) run.push(c.textContent); continue; }
      if (c.nodeType !== 1) continue;
      const tag = c.tagName.toUpperCase();
      if (tag === "IFRAME") {
        flush();
        try {
          const doc = c.contentDocument;
          if (doc && doc.body) walk(doc.body, ctx.concat(["iframe"]), {...flags, iframe: true});
        } catch (e) {}
        continue;
      }
      if (SKIP.has(tag) || !visible(c)) continue;
      const inShadow = flags.shadow || !!c.shadowRoot;
      const sub = {...flags, shadow: inShadow};
      const isBlock = BLOCKISH.has(tag) || !!c.shadowRoot;
      if (!isBlock && !hasBlockDescendant(c)) { run.push(c.innerText || c.textContent || ""); continue; }
      flush();
      const cctx = ctxFor(c, ctx);
      if (tag === "PRE" || !hasBlockDescendant(c) && !c.shadowRoot) emit(c.innerText || c.textContent, tag, cctx, sub);
      else walk(c, cctx, sub);
    }
    flush();
  }

  walk(document.body || document.documentElement, [], {});

  const html = document.documentElement.outerHTML;
  const q = (s) => { try { return document.querySelectorAll(s); } catch (e) { return []; } };
  const visibleCount = (s) => [...q(s)].filter(visible).length;
  const gen = (document.querySelector('meta[name="generator" i]') || {}).content || "";
  const scripts = [...document.scripts].map((s) => s.src).filter(Boolean).slice(0, 120);
  let jsonldFree = null;
  for (const s of q('script[type="application/ld+json"]')) {
    const m = (s.textContent || "").match(/"isAccessibleForFree"\s*:\s*"?(false|true)"?/i);
    if (m) jsonldFree = m[1].toLowerCase() === "true";
  }
  const reactRoot = [...q("#root, #app, #__next, [data-reactroot], body > div")].some((el) => Object.keys(el).some((k) => k.startsWith("__react")));
  const cmpSel = ["#onetrust-banner-sdk", "#onetrust-consent-sdk", "#CybotCookiebotDialog", "#didomi-host", ".qc-cmp2-container",
    "#usercentrics-root", "#truste-consent-track", ".cc-window", "#cookie-law-info-bar", ".fc-consent-root", "#sp_message_container",
    "[id^=sp_message_container]", "#cmpbox", ".osano-cm-window", "#iubenda-cs-banner", ".cky-consent-container", "#axeptio_overlay",
    "#cookiescript_injected", ".termly-styles-root", "#hs-eu-cookie-confirmation", "#borlabs-cookie", "#moove_gdpr_cookie_info_bar"];
  const cmpVisible = cmpSel.filter((s) => visibleCount(s) > 0);
  const fixedCookie = [...q("body *")].slice(0, 6000).some((el) => {
    const st = getComputedStyle(el);
    if (st.position !== "fixed" && st.position !== "sticky") return false;
    const t = (el.innerText || "").slice(0, 800).toLowerCase();
    return /cookie|consent|gdpr|privacy settings|datenschutz|cookies/.test(t) && visible(el);
  });
  const tables = [...q("table")].filter((t) => t.rows && t.rows.length >= 3);
  const tableChars = tables.reduce((n, t) => n + (t.innerText || "").length, 0);
  const codeBlocks = [...q("pre, pre code, .highlight, .codehilite")].filter((e) => (e.innerText || "").split("\n").length >= 3);
  const iframes = [...q("iframe")].map((f) => f.src || "");
  return {
    blocks, totalChars, shadowChars, iframeChars,
    title: document.title,
    htmlLang: document.documentElement.lang || "",
    dir: document.documentElement.dir || (document.body && document.body.dir) || "",
    htmlLength: html.length,
    nodes: document.getElementsByTagName("*").length,
    generator: gen,
    scripts,
    sig: {
      next: !!window.__NEXT_DATA__ || !!document.querySelector("#__NEXT_DATA__") || !!window.next || html.includes("/_next/static/"),
      nuxt: !!window.__NUXT__ || !!window.$nuxt || html.includes("/_nuxt/"),
      angular: !!document.querySelector("[ng-version]") || !!window.ng,
      react: reactRoot || !!window.React,
      vue: !!window.__VUE__ || !!document.querySelector("[data-v-app]") || !!window.Vue,
      gatsby: !!document.querySelector("#___gatsby"),
      astro: !!document.querySelector("astro-island") || /astro/i.test(gen),
      amp: document.documentElement.hasAttribute("amp") || document.documentElement.hasAttribute("⚡"),
      shopify: !!window.Shopify || html.includes("cdn.shopify.com"),
      wix: /wix/i.test(gen) || html.includes("static.wixstatic.com"),
      squarespace: !!(window.Static && window.Static.SQUARESPACE_CONTEXT) || html.includes("squarespace.com"),
      webflow: !!document.documentElement.getAttribute("data-wf-page"),
      ghost: /ghost/i.test(gen),
      drupal: !!window.Drupal || /drupal/i.test(gen),
      wordpress: /wordpress/i.test(gen) || html.includes("/wp-content/") || html.includes("/wp-includes/"),
      mediawiki: !!window.mw || /mediawiki/i.test(gen),
      forum: !!window.Discourse || /discourse|phpbb|vbulletin|xenforo|invision|smf/i.test(gen) || html.includes("phpBB") || html.includes("xenforo"),
      docs: /docusaurus|mkdocs|sphinx|gitbook|vitepress|hugo.*docsy|mintlify|readme\.io|nextra|starlight/i.test(gen) || html.includes("docusaurus") || html.includes("mkdocs") || !!window.DOCUMENTATION_OPTIONS || html.includes("gitbook"),
    },
    tcf: typeof window.__tcfapi === "function",
    cmpVisible, fixedCookie,
    jsonldFree,
    paywallHint: visibleCount('[class*="paywall" i], [id*="paywall" i], .tp-modal, .tp-container-inner, [class*="piano" i], [class*="meter" i][class*="wall" i], [data-testid*="paywall" i]') > 0,
    loginHint: visibleCount('input[type="password"]') > 0,
    tableChars, tableCount: tables.length,
    codeBlocks: codeBlocks.length, codeChars: codeBlocks.reduce((n, e) => n + (e.innerText || "").length, 0),
    math: q(".MathJax, mjx-container, .katex, math, .mwe-math-element").length,
    video: q("video").length + iframes.filter((s) => /youtube|youtu\.be|vimeo|dailymotion|player\.|wistia|jwplayer|brightcove|twitch/i.test(s)).length,
    audio: q("audio").length + iframes.filter((s) => /spotify|soundcloud|podcast|anchor\.fm|buzzsprout|libsyn|megaphone|simplecast|podbean|audioboom/i.test(s)).length,
    // images are not downloaded during fetch, so judge size from attributes and skip icon-like files
    images: [...q("img")].filter((i) => {
      const src = i.currentSrc || i.getAttribute("src") || i.getAttribute("data-src") || "";
      const w = parseInt(i.getAttribute("width") || "0", 10);
      return src && !/icon|logo|sprite|pixel|avatar|badge|emoji|\.svg|data:image\/gif/i.test(src) && (w === 0 || w >= 150) && visible(i);
    }).length,
    crossOriginIframes: iframes.length,
  };
})()
