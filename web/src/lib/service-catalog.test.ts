import assert from "node:assert/strict";
import test from "node:test";
import { filterServices, serviceCatalog } from "./service-catalog";
test("service suggestions match names, aliases and domains without inventing unknown services", () => {
  assert.equal(
    filterServices("  OPENAI ")[0]?.url,
    "https://status.openai.com/",
  );
  assert.equal(filterServices("claude")[0]?.name, "Anthropic");
  assert.equal(filterServices("Amazon Web Services")[0]?.name, "AWS");
  assert.equal(filterServices("supabase.com")[0]?.name, "Supabase");
  assert.equal(filterServices("chatgpt")[0]?.name, "OpenAI");
  assert.equal(filterServices("monday")[0]?.url, "https://status.monday.com/");
  assert.deepEqual(filterServices("unlisted-private-service"), []);
  assert.equal(serviceCatalog.length, 67);
  assert.equal(
    new Set(serviceCatalog.map((s) => s.url)).size,
    serviceCatalog.length,
  );
});
