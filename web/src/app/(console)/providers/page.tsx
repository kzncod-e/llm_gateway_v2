import type { Metadata } from "next";

import { ProvidersView } from "@/features/providers/providers-view";

export const metadata: Metadata = { title: "Providers" };

export default function Page() {
  return <ProvidersView />;
}
