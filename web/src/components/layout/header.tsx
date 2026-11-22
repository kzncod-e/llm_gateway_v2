"use client";

import { LogOut, Menu, Settings, UserRound } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { useLogout, useMe } from "@/features/auth/api";

import { Logo } from "./logo";
import { SidebarNav } from "./sidebar";

function initials(name: string, email: string) {
  const src = name.trim() || email;
  return src
    .split(/[\s@.]+/)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? "")
    .join("");
}

export function Header() {
  const { data } = useMe();
  const logout = useLogout();
  const [open, setOpen] = useState(false);
  const user = data?.user;

  return (
    <header className="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-border bg-background/95 px-4 backdrop-blur-sm lg:px-6">
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>
          <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Open navigation">
            <Menu />
          </Button>
        </SheetTrigger>
        <SheetContent side="left" className="w-64 border-sidebar-border bg-sidebar p-0">
          <SheetHeader className="h-14 justify-center border-b border-sidebar-border px-4">
            <SheetTitle className="sr-only">Navigation</SheetTitle>
            <Logo />
          </SheetHeader>
          <SidebarNav onNavigate={() => setOpen(false)} />
        </SheetContent>
      </Sheet>
      <div className="lg:hidden">
        <Logo />
      </div>
      <div className="ml-auto flex items-center gap-3">
        {user ? (
          <span className="hidden rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground sm:inline">{user.role === "admin" ? "Administrator" : "Member"}</span>
        ) : null}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" className="flex items-center gap-2 rounded-md px-1.5 py-1 hover:bg-accent" aria-label="Account menu">
              <span className="flex size-7 items-center justify-center rounded-md bg-elevated text-[11px] font-semibold text-sand">
                {user ? initials(user.name, user.email) : <UserRound className="size-3.5" />}
              </span>
              <span className="hidden text-left leading-tight md:block">
                <span className="block text-xs font-medium text-foreground">{user?.name || "—"}</span>
                <span className="block text-[11px] text-muted-foreground">{user?.email}</span>
              </span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuLabel className="text-xs text-muted-foreground">{user?.email}</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link href="/settings">
                <Settings /> Settings
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => logout.mutate()}>
              <LogOut /> Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
