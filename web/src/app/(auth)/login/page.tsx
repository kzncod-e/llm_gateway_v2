import type { Metadata } from "next";
import { Suspense } from "react";

import { Logo } from "@/components/layout/logo";

import { LoginForm } from "./login-form";

export const metadata: Metadata = { title: "Sign in" };

export default function LoginPage() {
  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <Logo className="mb-8" />
        <div className="rounded-lg border border-border bg-card p-6">
          <h1 className="text-base font-semibold text-foreground">Sign in</h1>
          <p className="mt-1 text-xs text-muted-foreground">Use your console account to manage providers, teams and usage.</p>
          <Suspense>
            <LoginForm />
          </Suspense>
        </div>
        <p className="mt-6 text-center text-[11px] text-muted-foreground">One API. Multiple Providers. Controlled Cost. Reliable Routing.</p>
      </div>
    </div>
  );
}
