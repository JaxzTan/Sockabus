// Who is signed in (the Google email from the session cookie), for the sidebar. Behind the proxy gate like every /api/* route.
import { cookies } from "next/headers";
import { authOff, readSession, SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  if (authOff()) return Response.json({ user: null, gate: false });
  const user = readSession((await cookies()).get(SESSION_COOKIE)?.value);
  return Response.json({ user, gate: true });
}
