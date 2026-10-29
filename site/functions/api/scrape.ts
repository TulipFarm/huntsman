interface Env {
  HUNTSMAN_URL?: string;
  HUNTSMAN_TOKEN?: string;
}

const MAX_URL = 2048;
const MAX_MARKDOWN = 60_000;
const SCRAPE_TIMEOUT_MS = 30_000;
const RATE_LIMIT = 10;
const RATE_WINDOW_MS = 60_000;

// Per isolate, so only a backstop; the zone's rate limiting rule on huntsman.tulipfarm.ai is the real limit.
const recent = new Map<string, number[]>();

function limited(ip: string) {
  const now = Date.now();
  const hits = (recent.get(ip) ?? []).filter((t) => now - t < RATE_WINDOW_MS);
  const blocked = hits.length >= RATE_LIMIT;
  if (!blocked) hits.push(now);
  if (recent.size > 10_000) recent.clear();
  recent.set(ip, hits);
  return blocked;
}

function normalize(raw: unknown) {
  if (typeof raw !== 'string') return null;
  let value = raw.trim();
  if (!value || value.length > MAX_URL) return null;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(value)) value = `https://${value}`;
  try {
    const url = new URL(value);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
    if (url.username || url.password || !url.hostname.includes('.')) return null;
    return url.toString();
  } catch {
    return null;
  }
}

// Without JavaScript the form posts here directly, so answer it with plain text instead of JSON.
function reply(body: Record<string, unknown>, status: number, asText: boolean) {
  if (asText) {
    const text = typeof body.markdown === 'string' ? body.markdown : String(body.error ?? '');
    return new Response(text, { status, headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
  }
  return Response.json(body, { status, headers: { 'Cache-Control': 'no-store' } });
}

type ScrapeResponse = {
  success?: boolean;
  error?: string;
  cached?: boolean;
  data?: {
    markdown?: string;
    links?: string[];
    metadata?: { title?: string; statusCode?: number; engine?: string };
  };
};

export const onRequestPost: PagesFunction<Env> = async ({ request, env }) => {
  const asText = !(request.headers.get('content-type') ?? '').includes('application/json');

  // The zone's rate limiting rule only sees huntsman.tulipfarm.ai, so don't spend scrapes on *.pages.dev.
  if (new URL(request.url).hostname.endsWith('.pages.dev')) {
    return reply({ error: 'Try the live demo on https://huntsman.tulipfarm.ai.' }, 403, asText);
  }

  let input: unknown;
  try {
    input = asText ? (await request.formData()).get('url') : ((await request.json()) as { url?: unknown }).url;
  } catch {
    return reply({ error: 'Send a JSON body like {"url": "https://example.com"}.' }, 400, asText);
  }

  const url = normalize(input);
  if (!url) return reply({ error: 'Enter a full web address, like https://example.com.' }, 400, asText);

  const base = env.HUNTSMAN_URL;
  if (!base) return reply({ error: 'The live demo isn’t connected to a Huntsman server right now.' }, 503, asText);

  if (limited(request.headers.get('CF-Connecting-IP') ?? 'unknown')) {
    return reply({ error: 'That’s 10 scrapes in a minute. Wait a moment, or run your own Huntsman below.' }, 429, asText);
  }

  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (env.HUNTSMAN_TOKEN) headers.Authorization = `Bearer ${env.HUNTSMAN_TOKEN}`;

  const started = performance.now();
  let res: Response;
  try {
    res = await fetch(new URL('/v1/scrape', base), {
      method: 'POST',
      headers,
      body: JSON.stringify({ url, formats: ['markdown'], onlyMainContent: true, timeout: SCRAPE_TIMEOUT_MS }),
      signal: AbortSignal.timeout(SCRAPE_TIMEOUT_MS + 5_000),
    });
  } catch {
    return reply({ error: 'Couldn’t reach the Huntsman server. Try again in a moment.' }, 502, asText);
  }
  const ms = Math.round(performance.now() - started);
  const body = (await res.json().catch(() => null)) as ScrapeResponse | null;

  if (res.status === 202) return reply({ error: 'That page took longer than 30 seconds to load.' }, 504, asText);
  if (!res.ok || !body?.success || !body.data) {
    const error = body?.error ? `Huntsman couldn’t scrape that page: ${body.error}` : 'Huntsman couldn’t scrape that page.';
    return reply({ error }, res.status >= 500 ? 502 : 422, asText);
  }

  const markdown = body.data.markdown ?? '';
  return reply(
    {
      url,
      ms,
      cached: Boolean(body.cached),
      title: body.data.metadata?.title ?? '',
      statusCode: body.data.metadata?.statusCode ?? null,
      engine: body.data.metadata?.engine ?? '',
      links: body.data.links?.length ?? 0,
      chars: markdown.length,
      truncated: markdown.length > MAX_MARKDOWN,
      markdown: markdown.slice(0, MAX_MARKDOWN),
    },
    200,
    asText,
  );
};
