import { Activity, BarChart3, Boxes, FlaskConical, LayoutDashboard, ScrollText, Settings, Users, type LucideIcon } from "lucide-react";

export interface NavItem {
  href: string;
  label: string;
  icon: LucideIcon;
}

export const NAV: { group: string; items: NavItem[] }[] = [
  {
    group: "Monitor",
    items: [
      { href: "/overview", label: "Overview", icon: LayoutDashboard },
      { href: "/usage", label: "Usage & Analytics", icon: BarChart3 },
      { href: "/requests", label: "Request Logs", icon: ScrollText },
      { href: "/reliability", label: "Reliability", icon: Activity },
    ],
  },
  {
    group: "Build",
    items: [{ href: "/playground", label: "Playground", icon: FlaskConical }],
  },
  {
    group: "Configure",
    items: [
      { href: "/providers", label: "Providers", icon: Boxes },
      { href: "/teams", label: "Teams", icon: Users },
      { href: "/settings", label: "Settings", icon: Settings },
    ],
  },
];
