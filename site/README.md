# huntsman.tulipfarm.ai

A static Astro site on Cloudflare Pages. The live scrape demo posts to `/api/scrape`, a Pages Function in
`functions/api/scrape.ts` that calls Huntsman server-side, so the server URL and token never reach the browser.

```sh
npm run dev       # site only; the demo answers 404 here
npm run preview   # site and function via wrangler; put HUNTSMAN_URL and HUNTSMAN_TOKEN in .dev.vars
npm run deploy    # build and deploy to the tulipfarm-huntsman Pages project
```

Set the demo's server once per project. These are encrypted secrets, not build variables:

```sh
npx wrangler pages secret put HUNTSMAN_URL
npx wrangler pages secret put HUNTSMAN_TOKEN
```

The demo only runs on huntsman.tulipfarm.ai; `*.pages.dev` answers 403 so it can't bypass the zone's rate limiting
rule (`huntsman live demo`: 3 requests to `/api/scrape` per 10 s per IP, then a 10 s block).
