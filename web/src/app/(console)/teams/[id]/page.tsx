import type { Metadata } from "next";
import { Suspense } from "react";

import { LoadingRows } from "@/components/common/states";
import { TeamDetailView } from "@/features/teams/team-detail-view";

export const metadata: Metadata = { title: "Team" };

async function Detail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <TeamDetailView id={id} />;
}

export default function Page({ params }: PageProps<"/teams/[id]">) {
  return (
    <Suspense fallback={<LoadingRows rows={8} />}>
      <Detail params={params} />
    </Suspense>
  );
}
