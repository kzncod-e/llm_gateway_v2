import type { Metadata } from "next";

import { RequestsView } from "@/features/requests/requests-view";

export const metadata: Metadata = { title: "Request Logs" };

export default function Page() {
  return <RequestsView />;
}
