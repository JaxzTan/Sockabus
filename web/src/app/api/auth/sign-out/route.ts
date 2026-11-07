import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { authEvent, readSession, SESSION_COOKIE } from "@/lib/auth";

export async function POST() {
  authEvent("sign_out", { email: readSession((await cookies()).get(SESSION_COOKIE)?.value) ?? undefined });
  const res = NextResponse.json({ ok: true });
  res.cookies.set(SESSION_COOKIE, "", { httpOnly: true, sameSite: "lax", path: "/", maxAge: 0 });
  return res;
}
