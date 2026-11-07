import type { Metadata } from "next";
import { Suspense } from "react";
import { Loading } from "@/components/ui";
import { OverviewView } from "./view";

export const metadata: Metadata = { title: "Dashboard" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <OverviewView />
    </Suspense>
  );
}
