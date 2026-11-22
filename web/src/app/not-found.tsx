import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 text-center">
      <p className="label-caps">404</p>
      <h1 className="text-lg font-semibold">Page not found</h1>
      <Button variant="outline" size="sm" asChild>
        <Link href="/overview">Back to overview</Link>
      </Button>
    </div>
  );
}
