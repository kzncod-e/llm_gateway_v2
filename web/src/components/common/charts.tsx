"use client";

import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

export const CHART_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)", "var(--chart-6)"];

const axis = { stroke: "var(--muted-foreground)", fontSize: 11, tickLine: false, axisLine: false } as const;

function TooltipBox({ active, payload, label, format, labelFormat }: {
  active?: boolean;
  payload?: { name?: string; value?: number; color?: string }[];
  label?: string | number;
  format: (v: number) => string;
  labelFormat?: (l: string) => string;
}) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-md border border-border bg-popover px-3 py-2 text-xs shadow-lg shadow-black/30">
      {label !== undefined ? <div className="mb-1 text-muted-foreground">{labelFormat ? labelFormat(String(label)) : label}</div> : null}
      {payload.map((p) => (
        <div key={p.name} className="flex items-center gap-2">
          <span className="size-2 rounded-sm" style={{ background: p.color }} />
          <span className="text-muted-foreground">{p.name}</span>
          <span className="tabular ml-auto pl-3 font-medium text-foreground">{format(Number(p.value ?? 0))}</span>
        </div>
      ))}
    </div>
  );
}

type Row = Record<string, string | number>;

export function TrendChart({
  data,
  xKey,
  series,
  format,
  xFormat,
  height = 220,
  stacked,
}: {
  data: Row[];
  xKey: string;
  series: { key: string; name: string; color?: string }[];
  format: (v: number) => string;
  xFormat: (v: string) => string;
  height?: number;
  stacked?: boolean;
}) {
  return (
    <div style={{ height }} className="w-full">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
          <defs>
            {series.map((s, i) => (
              <linearGradient key={s.key} id={`fill-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color ?? CHART_COLORS[i]} stopOpacity={0.22} />
                <stop offset="100%" stopColor={s.color ?? CHART_COLORS[i]} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid stroke="var(--border)" strokeDasharray="2 4" vertical={false} />
          <XAxis dataKey={xKey} {...axis} tickFormatter={xFormat} minTickGap={24} />
          <YAxis {...axis} width={56} tickFormatter={(v: number) => format(v)} />
          <Tooltip cursor={{ stroke: "var(--border)" }} content={<TooltipBox format={format} labelFormat={xFormat} />} />
          {series.map((s, i) => (
            <Area
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.name}
              stackId={stacked ? "a" : undefined}
              stroke={s.color ?? CHART_COLORS[i]}
              strokeWidth={1.5}
              fill={`url(#fill-${s.key})`}
              isAnimationActive={false}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

export function HBarChart({ data, format, height }: { data: { label: string; value: number }[]; format: (v: number) => string; height?: number }) {
  const h = height ?? Math.max(120, data.length * 32);
  return (
    <div style={{ height: h }} className="w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} layout="vertical" margin={{ top: 4, right: 12, left: 4, bottom: 4 }}>
          <CartesianGrid stroke="var(--border)" strokeDasharray="2 4" horizontal={false} />
          <XAxis type="number" {...axis} tickFormatter={(v: number) => format(v)} />
          <YAxis type="category" dataKey="label" {...axis} width={150} />
          <Tooltip cursor={{ fill: "var(--accent)" }} content={<TooltipBox format={format} />} />
          <Bar dataKey="value" name="Value" radius={[0, 3, 3, 0]} isAnimationActive={false} barSize={14}>
            {data.map((_, i) => (
              <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

export function DonutChart({ data, format, height = 180 }: { data: { label: string; value: number }[]; format: (v: number) => string; height?: number }) {
  return (
    <div style={{ height }} className="w-full">
      <ResponsiveContainer width="100%" height="100%">
        <PieChart>
          <Tooltip content={<TooltipBox format={format} />} />
          <Pie data={data} dataKey="value" nameKey="label" innerRadius="62%" outerRadius="90%" paddingAngle={2} stroke="var(--card)" isAnimationActive={false}>
            {data.map((_, i) => (
              <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
            ))}
          </Pie>
        </PieChart>
      </ResponsiveContainer>
    </div>
  );
}
