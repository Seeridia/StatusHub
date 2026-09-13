import test from "node:test";
import assert from "node:assert/strict";
const memory = new Map<string, string>();
Object.defineProperty(globalThis, "sessionStorage", {
  value: {
    getItem: (key: string) => memory.get(key) || null,
    setItem: (key: string, value: string) => memory.set(key, value),
    removeItem: (key: string) => memory.delete(key),
  },
});
Object.defineProperty(globalThis, "location", { value: { search: "" } });
const { configure, createWriter, APIError, allPages } = await import("./api");

test("uncertain write retries reuse idempotency key; changed payload gets a fresh key", async () => {
  configure({ tenant: "acme", token: "test-only", csrf: "csrf-test" });
  const requests: { url: string; init: RequestInit }[] = [];
  globalThis.fetch = async (url, init) => {
    requests.push({ url: String(url), init: init! });
    if (requests.length === 1) throw new TypeError("Network disconnected");
    return Response.json({ id: "created" });
  };
  const write = createWriter();
  await assert.rejects(write("/subscriptions", { name: "same" }));
  await write("/subscriptions", { name: "same" });
  await write("/subscriptions", { name: "different" });
  const headers = requests.map((r) => new Headers(r.init.headers));
  assert.equal(
    headers[0].get("Idempotency-Key"),
    headers[1].get("Idempotency-Key"),
  );
  assert.notEqual(
    headers[1].get("Idempotency-Key"),
    headers[2].get("Idempotency-Key"),
  );
  assert.equal(headers[0].get("Authorization"), "Bearer test-only");
  assert.equal(headers[0].get("X-CSRF-Token"), "csrf-test");
  assert.equal(requests[0].url, "/v1/tenants/acme/subscriptions");
});
test("selector lookup consumes cursor pages and null lists without dropping options", async () => {
  let count = 0;
  globalThis.fetch = async (url) => {
    count++;
    if (count === 1)
      return Response.json({ data: [{ id: "one" }], next_cursor: "a+b=" });
    assert.ok(String(url).includes("cursor=a%2Bb%3D"));
    return Response.json({ data: null });
  };
  assert.deepEqual(await allPages("/endpoints"), [{ id: "one" }]);
});
test("permission rejection remains an actionable error", async () => {
  globalThis.fetch = async () =>
    Response.json({ detail: "forbidden" }, { status: 403 });
  await assert.rejects(
    createWriter()("/endpoints", {}),
    (error) => error instanceof APIError && error.status === 403,
  );
});
