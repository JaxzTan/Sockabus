import { redirect } from "next/navigation";

// "/" is normally redirected by src/proxy.ts (to /home when signed in, else /sign-in). This is only the
// fallback if the proxy is ever bypassed; /home is still behind the gate.
export default function Page() {
  redirect("/home");
}
