import type { Metadata } from "next";

import { ReliabilityView } from "@/features/reliability/reliability-view";

export const metadata: Metadata = { title: "Reliability" };

export default function Page() {
  return <ReliabilityView />;
}
