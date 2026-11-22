import type { Metadata } from "next";

import { TeamsView } from "@/features/teams/teams-view";

export const metadata: Metadata = { title: "Teams" };

export default function Page() {
  return <TeamsView />;
}
