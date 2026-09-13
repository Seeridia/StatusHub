import test from "node:test";
import assert from "node:assert/strict";
import { consumeStream, parseFrame, type StreamMessage } from "./stream";

test("SSE parsing preserves resume cursor, multiline data, and ignores heartbeat", () => {
  assert.deepEqual(parseFrame(": heartbeat"), {});
  assert.deepEqual(
    parseFrame("id: signed-cursor\nevent: update\ndata: first\ndata: second"),
    { id: "signed-cursor", event: "update", data: "first\nsecond" },
  );
  assert.equal(parseFrame("id: invalid\0cursor").id, undefined);
});
test("SSE tolerates split UTF-8 and CRLF, and reports normal EOF for reconnect", async () => {
  const encoder = new TextEncoder();
  const bytes = encoder.encode("id: cursor-1\r\ndata: 中文\r\n\r\n");
  const frames: StreamMessage[] = [];
  const stream = new ReadableStream({
    start(controller) {
      for (const byte of bytes) controller.enqueue(new Uint8Array([byte]));
      controller.close();
    },
  });
  await assert.rejects(
    consumeStream(new Response(stream), (frame) => frames.push(frame)),
    /事件流已断开/,
  );
  assert.deepEqual(frames, [{ id: "cursor-1", data: "中文" }]);
});
