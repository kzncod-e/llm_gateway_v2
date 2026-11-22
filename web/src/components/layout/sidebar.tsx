"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

import { Logo } from "./logo";
import { NAV } from "./nav";

export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <nav className="flex flex-1 flex-col gap-5 px-3 py-4" aria-label="Main">
      {NAV.map((g) => (
        <div key={g.group}>
          <div className="label-caps mb-1.5 px-2">{g.group}</div>
          <ul className="space-y-0.5">
            {g.items.map((item) => {
              const active = pathname === item.href || pathname.startsWith(item.href + "/");
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "group flex h-8 items-center gap-2.5 rounded-md px-2 text-[13px] text-sidebar-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                      active && "bg-sidebar-accent text-sidebar-accent-foreground",
                    )}
                  >
                    <item.icon className={cn("size-4 text-muted-foreground group-hover:text-sand", active && "text-sand")} />
                    {item.label}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar lg:flex">
      <div className="flex h-14 items-center border-b border-sidebar-border px-4">
        <Logo />
      </div>
      <SidebarNav />
      <div className="border-t border-sidebar-border px-4 py-3 text-[11px] text-muted-foreground">
        One API. Multiple providers.
      </div>
    </aside>
  );
}
