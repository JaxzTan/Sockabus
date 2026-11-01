import type { Metadata } from "next";
import { Suspense } from "react";
import { Loading } from "@/components/ui";
import { AutomateView } from "./view";

export const metadata: Metadata = { title: "Automate" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <AutomateView />
    </Suspense>
  );
}
