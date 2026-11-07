// In-process fixed-window limiter for the sign-in routes, which run on the Next server and never reach
// the Go API's limiter. One web container per deployment, so process memory is enough.
const windows = new Map<string, { start: number; count: number }>();
let lastSweep = 0;

// true when key may make another request in this window; limit requests per windowMs.
export function allow(key: string, limit: number, windowMs: number, now = Date.now()): boolean {
  if (now - lastSweep > windowMs) {
    for (const [k, w] of windows) if (now - w.start >= windowMs) windows.delete(k);
    lastSweep = now;
  }
  const w = windows.get(key);
  if (!w || now - w.start >= windowMs) {
    windows.set(key, { start: now, count: 1 });
    return true;
  }
  w.count++;
  return w.count <= limit;
}
