import { tr } from "./i18n";
export interface StreamMessage {
  id?: string;
  event?: string;
  data?: string;
}
export function parseFrame(frame: string): StreamMessage {
  const result: StreamMessage = {};
  const data: string[] = [];
  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
    if (field === "id" && !value.includes("\0")) result.id = value;
    if (field === "event") result.event = value;
    if (field === "data") data.push(value);
  }
  if (data.length) result.data = data.join("\n");
  return result;
}
export async function consumeStream(
  response: Response,
  onFrame: (frame: StreamMessage) => void,
) {
  if (!response.body)
    throw new Error(tr("\u54CD\u5E94\u6CA1\u6709\u4E8B\u4EF6\u6D41"));
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) throw new Error(tr("\u4E8B\u4EF6\u6D41\u5DF2\u65AD\u5F00"));
      buffer += decoder.decode(value, { stream: true });
      // Normalize only complete CRLF pairs; a chunk may end between CR and LF.
      buffer = buffer.replace(/\r\n/g, "\n");
      let boundary: number;
      while ((boundary = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        onFrame(parseFrame(frame));
      }
      if (buffer.length > 2000000)
        throw new Error(tr("\u4E8B\u4EF6\u6D41\u6D88\u606F\u8FC7\u5927"));
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
