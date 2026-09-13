import assert from "node:assert/strict";
import test from "node:test";
import i18n, { currentLanguage, tr } from "./i18n";

test("UI copy switches between English and Chinese", async () => {
  await i18n.changeLanguage("en");
  assert.equal(currentLanguage(), "en");
  assert.equal(tr("总览"), "Overview");
  assert.equal(
    tr("查看 {{value0}}", { value0: "OpenAI" }),
    "View OpenAI",
  );

  await i18n.changeLanguage("zh");
  assert.equal(currentLanguage(), "zh");
  assert.equal(tr("总览"), "总览");
  assert.equal(
    tr("查看 {{value0}}", { value0: "OpenAI" }),
    "查看 OpenAI",
  );
});
