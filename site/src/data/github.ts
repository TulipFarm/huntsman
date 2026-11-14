// Repo facts for the open-source badges, read from GitHub once per build. A failed request hides the badge
// instead of failing the build.
const api = 'https://api.github.com/repos/TulipFarm/huntsman';

async function get<T>(path: string): Promise<T | null> {
  try {
    const token = process.env.GITHUB_TOKEN;
    const res = await fetch(api + path, {
      signal: AbortSignal.timeout(4000),
      headers: { Accept: 'application/vnd.github+json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    });
    return res.ok ? ((await res.json()) as T) : null;
  } catch {
    return null;
  }
}

const [info, release] = await Promise.all([
  get<{ stargazers_count: number }>(''),
  get<{ tag_name: string }>('/releases/latest'),
]);

const compact = (n: number) => (n < 1000 ? String(n) : `${(n / 1000).toFixed(n < 10000 ? 1 : 0).replace(/\.0$/, '')}k`);

export const stars = info ? compact(info.stargazers_count) : null;
export const latest = release?.tag_name ?? null;
