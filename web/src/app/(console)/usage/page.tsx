import type { Metadata } from "next";

import { UsageView } from "@/features/analytics/usage-view";

export const metadata: Metadata = { title: "Usage & Analytics" };

export default function Page() {
  return <UsageView />;
}
