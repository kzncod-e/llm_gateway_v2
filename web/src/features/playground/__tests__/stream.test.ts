import { describe, expect, it } from "vitest";

import { sseEvents } from "../stream";

function streamOf(chunks: string[]) {
  const enc = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(c) {
      for (const ch of chunks) c.enqueue(enc.encode(ch));
      c.close();
    },
  });
}

async function collect(chunks: string[]) {
  const out: string[] = [];
  for await (const e of sseEvents(streamOf(chunks))) out.push(e);
  return out;
}

describe("sseEvents", () => {
  it("parses events split across network chunks", async () => {
    expect(await collect(['data: {"a"', ':1}\n\nda', "ta: [DONE]\n\n"])).toEqual(['{"a":1}', "[DONE]"]);
  });
  it("handles CRLF separators and ignores comments and other fields", async () => {
    expect(await collect([": ping\r\n\r\nevent: x\r\ndata: one\r\n\r\ndata: two\r\n\r\n"])).toEqual(["one", "two"]);
  });
  it("joins multi-line data", async () => {
    expect(await collect(["data: a\ndata: b\n\n"])).toEqual(["a\nb"]);
  });
});
