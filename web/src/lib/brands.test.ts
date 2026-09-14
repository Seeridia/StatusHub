import assert from "node:assert/strict";
import test from "node:test";
import {
  brandIconSources,
  brandIconSourcesForName,
  findEcosystemBrand,
} from "./brands";

test("known brands prefer Devicon and fall back to Simple Icons", () => {
  const github = findEcosystemBrand("GitHub");
  assert.ok(github);
  assert.deepEqual(brandIconSources(github), [
    "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/github/github-original.svg",
    "https://cdn.simpleicons.org/github?viewbox=auto",
  ]);
});

test("known brands still try Devicon before Simple Icons", () => {
  const airtable = findEcosystemBrand("Airtable");
  assert.ok(airtable);
  assert.deepEqual(brandIconSources(airtable), [
    "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/airtable/airtable-original.svg",
    "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/airtable/airtable-plain.svg",
    "https://cdn.simpleicons.org/airtable?viewbox=auto",
  ]);
});

test("brand aliases resolve while unknown vendors remain dynamic", () => {
  assert.equal(findEcosystemBrand("Grafana Cloud")?.name, "Grafana");
  assert.equal(findEcosystemBrand("Amazon Web Services")?.name, "AWS");
  assert.equal(findEcosystemBrand("Private status page"), undefined);
});

test("vendors outside the catalog try both CDNs", () => {
  assert.deepEqual(brandIconSourcesForName("Example Cloud"), [
    "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/examplecloud/examplecloud-original.svg",
    "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons/examplecloud/examplecloud-plain.svg",
    "https://cdn.simpleicons.org/examplecloud?viewbox=auto",
  ]);
  assert.deepEqual(brandIconSourcesForName("云服务"), []);
});
