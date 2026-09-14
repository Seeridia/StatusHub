import assert from "node:assert/strict";
import test from "node:test";
import i18n, { currentLanguage, resolveLanguage, tr } from "./i18n";

test("language defaults follow supported browser preferences in order", () => {
  assert.equal(resolveLanguage(null, ["zh-CN", "en-US"]), "zh");
  assert.equal(resolveLanguage(null, ["en-GB", "zh-CN"]), "en");
  assert.equal(resolveLanguage("system", ["fr-FR", "zh-TW", "en"]), "zh");
  assert.equal(resolveLanguage(null, ["zh-Hant-HK"]), "zh");
  assert.equal(resolveLanguage(null, [" ZH_cn "]), "zh");
});

test("unsupported or missing browser languages fall back to English", () => {
  assert.equal(resolveLanguage(null, ["fr-FR", "ja-JP"]), "en");
  assert.equal(resolveLanguage(null, []), "en");
  assert.equal(resolveLanguage("invalid", ["zh-CN"]), "zh");
});

test("explicit choices override the browser and system restores detection", () => {
  assert.equal(resolveLanguage("en", ["zh-CN"]), "en");
  assert.equal(resolveLanguage("zh", ["en-US"]), "zh");
  assert.equal(resolveLanguage("system", ["zh-CN"]), "zh");
});

test("UI copy switches between English and Chinese", async () => {
  await i18n.changeLanguage("en");
  assert.equal(currentLanguage(), "en");
  assert.equal(tr("总览"), "Overview");
  assert.equal(tr("查看 {{value0}}", { value0: "OpenAI" }), "View OpenAI");

  await i18n.changeLanguage("zh");
  assert.equal(currentLanguage(), "zh");
  assert.equal(tr("总览"), "总览");
  assert.equal(tr("查看 {{value0}}", { value0: "OpenAI" }), "查看 OpenAI");
});
