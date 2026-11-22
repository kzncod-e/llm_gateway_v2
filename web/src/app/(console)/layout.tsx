import { Suspense } from "react";

import { LoadingRows } from "@/components/common/states";
import { Header } from "@/components/layout/header";
import { Sidebar } from "@/components/layout/sidebar";

function ShellFallback() {
  return <div className="h-14 border-b border-border" />;
}

export default function ConsoleLayout({ children }: LayoutProps<"/">) {
  return (
    <div className="flex min-h-screen">
      <Suspense fallback={<div className="hidden w-56 shrink-0 border-r border-sidebar-border bg-sidebar lg:block" />}>
        <Sidebar />
      </Suspense>
      <div className="flex min-w-0 flex-1 flex-col">
        <Suspense fallback={<ShellFallback />}>
          <Header />
        </Suspense>
        <main className="mx-auto w-full max-w-[1400px] flex-1 px-4 py-6 lg:px-8">
          {/* Console pages depend on the session and the current time, so they render per visit. */}
          <Suspense fallback={<LoadingRows rows={8} />}>{children}</Suspense>
        </main>
      </div>
    </div>
  );
}
