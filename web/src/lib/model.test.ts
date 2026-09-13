import test from "node:test";
import assert from "node:assert/strict";
import { buildScopes, editableScopes, stale } from "./model";
import type { Vendor } from "./types";

test("vendor and event selections form an intersection, not broad OR scopes", () => {
  const result = buildScopes(
    ["a", "b"],
    ["incident.created", "incident.resolved"],
  );
  assert.equal(result.length, 4);
  assert.ok(result.every((s) => s.vendor_id && s.event_kind));
  assert.deepEqual(buildScopes([], []), []);
  assert.deepEqual(buildScopes(["a"], []), [{ vendor_id: "a" }]);
});
test("editor refuses conditions that would broaden or discard existing scopes", () => {
  assert.equal(
    editableScopes([{ vendor_id: "a", component_key: "api" }]),
    false,
  );
  assert.equal(
    editableScopes([
      { vendor_id: "a", event_kind: "incident.created" },
      { vendor_id: "b", event_kind: "incident.resolved" },
    ]),
    false,
  );
  assert.equal(
    editableScopes([{ vendor_id: "a" }, { event_kind: "incident.created" }]),
    false,
  );
  assert.equal(
    editableScopes(
      buildScopes(["a", "b"], ["incident.created", "incident.resolved"]),
    ),
    true,
  );
});
test("an operational vendor with stale or unhealthy collection is not fresh", () => {
  const now = Date.parse("2026-09-13T00:00:00Z");
  const vendor = {
    status: "operational",
    source_health_state: "healthy",
    last_successful_at: new Date(now - 61_000).toISOString(),
    collection: {
      state: "fresh",
      reason: "on_schedule",
      fresh_until: new Date(now + 60_000).toISOString(),
      evaluated_at: new Date(now).toISOString(),
    },
  } as Vendor;
  assert.equal(stale(vendor, now), false);
  assert.equal(stale({ ...vendor, last_successful_at: undefined }, now), false);
  assert.equal(
    stale(
      {
        ...vendor,
        collection: {
          ...vendor.collection!,
          fresh_until: new Date(now - 1).toISOString(),
        },
      },
      now,
    ),
    true,
  );
  assert.equal(
    stale(
      { ...vendor, collection: { ...vendor.collection!, state: "stale" } },
      now,
    ),
    true,
  );
  assert.equal(
    stale(
      {
        ...vendor,
        last_successful_at: new Date(now - 15 * 60_000).toISOString(),
      },
      now,
    ),
    false,
  );
  assert.equal(
    stale(
      { ...vendor, collection: { ...vendor.collection!, state: "unknown" } },
      now,
    ),
    false,
  );
  assert.equal(
    stale(
      { ...vendor, collection: { ...vendor.collection!, state: "disabled" } },
      now,
    ),
    false,
  );
});
