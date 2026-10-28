// Capacity search on 1,000 random URLs from the test split of huntsman-scrape-dataset-v1.
// Raw results: benchmark/results/matrix/<machine>/<engine>/ramp.json

export type EngineResult = {
  engine: string;
  // Highest number of pages in flight that passed; null when even the lowest level tested failed.
  maxConcurrency: number | null;
  firstFailure: number | null;
  failureReason?: string;
  pagesPerSecond?: number;
  successRate?: number;
  note?: string;
};

export type Machine = {
  id: 'small' | 'medium' | 'large';
  label: string;
  instance: string;
  vcpu: number;
  memoryGB: number;
  results: EngineResult[];
};


export const machines: Machine[] = [
  {
    id: 'small',
    label: 'Small',
    instance: 'c7g.large',
    vcpu: 2,
    memoryGB: 4,
    results: [
      { engine: 'Huntsman', maxConcurrency: 45, firstFailure: 46, failureReason: 'latency', pagesPerSecond: 4.58, successRate: 90.9 },
      { engine: 'Firecrawl', maxConcurrency: 8, firstFailure: 9, failureReason: 'out of memory', pagesPerSecond: 0.91, successRate: 92.5 },
      { engine: 'Crawl4AI', maxConcurrency: null, firstFailure: 2, failureReason: 'out of memory', note: 'Crashed or ran out of memory even at 2 pages at once' },
    ],
  },
  {
    id: 'medium',
    label: 'Medium',
    instance: 'c7g.xlarge',
    vcpu: 4,
    memoryGB: 8,
    results: [
      { engine: 'Huntsman', maxConcurrency: 75, firstFailure: 76, failureReason: 'latency', pagesPerSecond: 7.74, successRate: 91.8 },
      { engine: 'Firecrawl', maxConcurrency: 15, firstFailure: 16, failureReason: 'out of memory', pagesPerSecond: 1.58, successRate: 92.2 },
      { engine: 'Crawl4AI', maxConcurrency: 15, firstFailure: 16, failureReason: 'latency', pagesPerSecond: 1.35, successRate: 80.6 },
    ],
  },
  {
    id: 'large',
    label: 'Large',
    instance: 'c7g.2xlarge',
    vcpu: 8,
    memoryGB: 16,
    results: [
      { engine: 'Huntsman', maxConcurrency: 129, firstFailure: 130, failureReason: 'latency', pagesPerSecond: 11.54, successRate: 90.8 },
      { engine: 'Firecrawl', maxConcurrency: 43, firstFailure: 44, failureReason: 'latency', pagesPerSecond: 2.9, successRate: 92.3 },
      { engine: 'Crawl4AI', maxConcurrency: 22, firstFailure: 23, failureReason: 'latency', pagesPerSecond: 1.62, successRate: 81.8 },
    ],
  },
];

export const ratio = (m: Machine, key: 'maxConcurrency' | 'pagesPerSecond') => {
  const h = m.results.find((r) => r.engine === 'Huntsman')?.[key];
  const f = m.results.find((r) => r.engine === 'Firecrawl')?.[key];
  return h && f ? h / f : null;
};
