"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2 } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { Field } from "@/components/common/field";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useLogin } from "@/features/auth/api";
import { ApiError } from "@/lib/api";

const schema = z.object({
  email: z.email("Enter a valid email address"),
  password: z.string().min(1, "Password is required"),
});
type Values = z.infer<typeof schema>;

/** Only same-site relative paths are accepted as a post-login destination. */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/overview";
  return next;
}

export function LoginForm() {
  const router = useRouter();
  const params = useSearchParams();
  const login = useLogin();
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { email: "", password: "" } });

  const onSubmit = form.handleSubmit((values) =>
    login.mutate(values, {
      onSuccess: () => router.replace(safeNext(params.get("next"))),
    }),
  );

  const err = login.error instanceof ApiError ? login.error.message : login.error ? "Unable to reach the server." : null;

  return (
    <form onSubmit={onSubmit} className="mt-5 space-y-4" noValidate>
      {err ? (
        <Alert variant="destructive" className="border-danger/30 bg-danger/10 py-2">
          <AlertDescription className="text-xs text-danger">{err}</AlertDescription>
        </Alert>
      ) : null}
      <Field label="Email" htmlFor="email" error={form.formState.errors.email?.message}>
        <Input id="email" type="email" autoComplete="email" autoFocus {...form.register("email")} />
      </Field>
      <Field label="Password" htmlFor="password" error={form.formState.errors.password?.message}>
        <Input id="password" type="password" autoComplete="current-password" {...form.register("password")} />
      </Field>
      <Button type="submit" className="w-full" disabled={login.isPending}>
        {login.isPending ? <Loader2 className="animate-spin" /> : null}
        Sign in
      </Button>
    </form>
  );
}
