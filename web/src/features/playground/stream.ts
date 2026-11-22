// Calls the playground endpoint and incrementally parses OpenAI-style SSE.

import { parseError } from "@/lib/api";

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatParams {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
  top_p?: number;
  max_tokens?: number;
  stream: boolean;
}

export interface ChatUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  prompt_tokens_details?: { cached_tokens: number; cache_write_tokens?: number };
  completion_tokens_details?: { reasoning_tokens: number };
  cost_nanos?: number;
}

export interface ChatMeta {
  requestId: string | null;
  provider: string | null;
  model: string | null;
  attempts: string | null;
}

export interface ChatResult {
  content: string;
  finishReason: string | null;
  usage: ChatUsage | null;
  meta: ChatMeta;
  ttfbMs: number | null;
  totalMs: number;
  interrupted?: string;
}

function metaFrom(res: Response): ChatMeta {
  return {
    requestId: res.headers.get("x-request-id"),
    provider: res.headers.get("x-gateway-provider"),
    model: res.headers.get("x-gateway-model"),
    attempts: res.headers.get("x-gateway-attempts"),
  };
}

/** Parses an SSE byte stream and yields the JSON payload of each `data:` line. */
export async function* sseEvents(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buf.search(/\r?\n\r?\n/)) !== -1) {
        const block = buf.slice(0, idx);
        buf = buf.slice(idx).replace(/^\r?\n\r?\n/, "");
        const data = block
          .split(/\r?\n/)
          .filter((l) => l.startsWith("data:"))
          .map((l) => l.slice(5).trimStart())
          .join("\n");
        if (data) yield data;
      }
    }
  } finally {
    reader.releaseLock();
  }
}

export async function runChat(
  teamId: string,
  params: ChatParams,
  onDelta: (text: string) => void,
  signal: AbortSignal,
): Promise<ChatResult> {
  const started = performance.now();
  const res = await fetch(`/api/v1/playground/chat?team_id=${encodeURIComponent(teamId)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(params.stream ? { ...params, stream_options: { include_usage: true } } : params),
    signal,
  });
  if (!res.ok) throw await parseError(res);
  const meta = metaFrom(res);

  if (!params.stream) {
    const body = await res.json();
    const content: string = body.choices?.[0]?.message?.content ?? "";
    onDelta(content);
    return { content, finishReason: body.choices?.[0]?.finish_reason ?? null, usage: body.usage ?? null, meta, ttfbMs: null, totalMs: performance.now() - started };
  }

  let content = "";
  let finishReason: string | null = null;
  let usage: ChatUsage | null = null;
  let ttfbMs: number | null = null;
  let interrupted: string | undefined;
  let done = false;
  for await (const data of sseEvents(res.body!)) {
    if (data === "[DONE]") {
      done = true;
      break;
    }
    const chunk = JSON.parse(data);
    if (chunk.error) {
      interrupted = chunk.error.message ?? "stream interrupted";
      break;
    }
    if (chunk.usage) usage = chunk.usage;
    const choice = chunk.choices?.[0];
    if (choice?.delta?.content) {
      if (ttfbMs === null) ttfbMs = performance.now() - started;
      content += choice.delta.content;
      onDelta(choice.delta.content);
    }
    if (choice?.finish_reason) finishReason = choice.finish_reason;
  }
  if (!done && !interrupted) interrupted = "The stream ended before completion.";
  return { content, finishReason, usage, meta, ttfbMs, totalMs: performance.now() - started, interrupted };
}
