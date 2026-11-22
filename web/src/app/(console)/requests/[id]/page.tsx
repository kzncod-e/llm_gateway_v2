import type { Metadata } from "next";
import { Suspense } from "react";

import { LoadingRows } from "@/components/common/states";
import { RequestDetailView } from "@/features/requests/request-detail-view";

export const metadata: Metadata = { title: "Request" };

async function Detail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <RequestDetailView id={id} />;
}

export default function Page({ params }: PageProps<"/requests/[id]">) {
  return (
    <Suspense fallback={<LoadingRows rows={8} />}>
      <Detail params={params} />
    </Suspense>
  );
}
