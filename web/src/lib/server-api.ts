// Server-only helper for calling the Go API (cmd/sockabus). Never import this from a client component.
import "server-only";

// Read at request time, so the Docker image doesn't need a rebuild to change them.
const apiURL = () => process.env.SOCKABUS_API_URL ?? "http://localhost:8080";

// SOCKABUS_MODE=live guards every route with this bearer token until operator login exists (TRD §7).
const operatorToken = () => process.env.SOCKABUS_OPERATOR_TOKEN ?? "";

// Lookup uses its own key once per-customer API keys exist (FR-13); until then the operator token.
export const lookupToken = () => process.env.SOCKABUS_LOOKUP_TOKEN || operatorToken();

// The browser's IP as Caddy saw it: the right-most X-Forwarded-For entry (the one Caddy appended).
// Passed on to the Go API so its per-client rate limits count real clients, not this proxy.
export function clientIP(req: Request): string | null {
  const parts = (req.headers.get("x-forwarded-for") ?? "").split(",").map((s) => s.trim()).filter(Boolean);
  const ip = parts.at(-1) ?? "";
  return /^[0-9a-fA-F:.]{2,45}$/.test(ip) ? ip : null;
}

export async function goAPI(
  path: string,
  opts: { method?: string; body?: BodyInit; contentType?: string | null; token?: string; signal?: AbortSignal; clientIP?: string | null } = {},
): Promise<Response> {
  const headers = new Headers({ accept: "application/json" });
  if (opts.contentType) headers.set("content-type", opts.contentType);
  if (opts.clientIP) headers.set("x-forwarded-for", opts.clientIP);
  const token = opts.token ?? operatorToken();
  if (token) headers.set("authorization", `Bearer ${token}`);
  // A hung backend must not leave the console on "Loading…": give up and return an error instead.
  // Persona generation runs up to 5 min on the backend; everything else answers in seconds.
  const timeout = AbortSignal.timeout(path.includes("/personas/generate") ? 330_000 : 20_000);
  const signal = opts.signal ? AbortSignal.any([opts.signal, timeout]) : timeout;
  try {
    return await fetch(apiURL() + path, { method: opts.method ?? "GET", headers, body: opts.body, signal, cache: "no-store" });
  } catch {
    if (timeout.aborted) return Response.json({ error: "The Sockabus API took too long to answer." }, { status: 504 });
    return Response.json({ error: "The Sockabus API is not reachable. Is the Go server running?" }, { status: 502 });
  }
}
