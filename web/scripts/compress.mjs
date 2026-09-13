import { readdir, readFile, writeFile } from "node:fs/promises";
import { gzipSync } from "node:zlib";
const root = new URL(
  "../../internal/controlplane/assets/console/",
  import.meta.url,
);
async function visit(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const url = new URL(
      entry.name + (entry.isDirectory() ? "/" : ""),
      directory,
    );
    if (entry.isDirectory()) await visit(url);
    else if (/\.(js|css)$/.test(entry.name))
      await writeFile(
        new URL(entry.name + ".gz", directory),
        gzipSync(await readFile(url), { level: 9 }),
      );
  }
}
await visit(root);
