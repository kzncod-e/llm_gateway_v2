export function Logo({ className = "" }: { className?: string }) {
  return (
    <div className={`flex items-center gap-2.5 ${className}`}>
      <svg viewBox="0 0 24 24" className="size-6" aria-hidden>
        <rect x="1" y="1" width="22" height="22" rx="5" fill="var(--cs-sand)" />
        <path d="M6 7h4.5l3 5-3 5H6m12-10h-3.5M18 17h-3.5M13.5 12H18" stroke="#1c1c1a" strokeWidth="1.8" fill="none" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <div className="leading-tight">
        <div className="text-[13px] font-semibold tracking-tight text-foreground">LLM Gateway</div>
        <div className="text-[10px] text-muted-foreground">Control plane</div>
      </div>
    </div>
  );
}
