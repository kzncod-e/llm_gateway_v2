import type { Metadata } from "next";

import { PlaygroundView } from "@/features/playground/playground-view";

export const metadata: Metadata = { title: "Playground" };

export default function Page() {
  return <PlaygroundView />;
}
