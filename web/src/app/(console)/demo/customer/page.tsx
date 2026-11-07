import type { Metadata } from "next";
import { Suspense } from "react";
import { Loading } from "@/components/ui";
import { CustomerView } from "./view";

export const metadata: Metadata = { title: "Customer view" };

export default function Page() {
  return (
    <Suspense fallback={<Loading />}>
      <CustomerView />
    </Suspense>
  );
}
