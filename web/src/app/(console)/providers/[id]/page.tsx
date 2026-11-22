import type { Metadata } from "next";
import { Suspense } from "react";

import { LoadingRows } from "@/components/common/states";
import { ProviderDetailView } from "@/features/providers/provider-detail-view";

export const metadata: Metadata = { title: "Provider" };

async function Detail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <ProviderDetailView id={id} />;
}

export default function Page({ params }: PageProps<"/providers/[id]">) {
  return (
    <Suspense fallback={<LoadingRows rows={8} />}>
      <Detail params={params} />
    </Suspense>
  );
}
