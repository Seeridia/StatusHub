import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

const sourceRoot = path.resolve("src");
const localeFile = path.join(sourceRoot, "lib/i18n.ts");
const sourceFiles = [];

function collectFiles(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) collectFiles(filename);
    else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) {
      sourceFiles.push(filename);
    }
  }
}

function parse(filename) {
  const contents = fs.readFileSync(filename, "utf8");
  return ts.createSourceFile(
    filename,
    contents,
    ts.ScriptTarget.Latest,
    true,
    filename.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function propertyName(property) {
  const name = property.name;
  return name &&
    (ts.isIdentifier(name) ||
      ts.isStringLiteralLike(name) ||
      ts.isNumericLiteral(name))
    ? name.text
    : undefined;
}

collectFiles(sourceRoot);

const localeSource = parse(localeFile);
const translated = new Set();
function collectTranslations(node) {
  if (
    ts.isVariableDeclaration(node) &&
    ts.isIdentifier(node.name) &&
    node.name.text === "english" &&
    node.initializer &&
    ts.isObjectLiteralExpression(node.initializer)
  ) {
    for (const property of node.initializer.properties) {
      if (ts.isPropertyAssignment(property)) {
        const name = propertyName(property);
        if (name !== undefined) translated.add(name);
      }
    }
  }
  ts.forEachChild(node, collectTranslations);
}
collectTranslations(localeSource);

const used = new Map();
const unlocalized = [];
const han = /[\u3400-\u9fff]/;

for (const filename of sourceFiles) {
  if (filename === localeFile) continue;
  const source = parse(filename);

  function visit(node, insideTranslation = false) {
    const isTranslation =
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "tr";

    if (isTranslation) {
      const key = node.arguments[0];
      if (!key || !ts.isStringLiteralLike(key)) {
        const line =
          source.getLineAndCharacterOfPosition(node.getStart()).line + 1;
        unlocalized.push(
          `${path.relative(process.cwd(), filename)}:${line} tr() key must be a string literal`,
        );
      } else {
        used.set(
          key.text,
          `${path.relative(process.cwd(), filename)}:${source.getLineAndCharacterOfPosition(key.getStart()).line + 1}`,
        );
      }
    }

    if (!insideTranslation && !isTranslation) {
      let value;
      if (
        ts.isStringLiteralLike(node) ||
        ts.isNoSubstitutionTemplateLiteral(node)
      )
        value = node.text;
      else if (ts.isJsxText(node)) value = node.getText(source);
      if (value && han.test(value)) {
        const line =
          source.getLineAndCharacterOfPosition(node.getStart()).line + 1;
        unlocalized.push(
          `${path.relative(process.cwd(), filename)}:${line} ${JSON.stringify(value.trim())}`,
        );
      }
    }

    ts.forEachChild(node, (child) =>
      visit(child, insideTranslation || isTranslation),
    );
  }

  visit(source);
}

const missing = [...used].filter(
  ([key]) =>
    !translated.has(key) &&
    !(translated.has(`${key}_one`) && translated.has(`${key}_other`)),
);
if (missing.length || unlocalized.length) {
  if (missing.length) {
    console.error("Missing English translations:");
    for (const [key, location] of missing)
      console.error(`  ${location} ${JSON.stringify(key)}`);
  }
  if (unlocalized.length) {
    console.error("Unlocalized Chinese UI text:");
    for (const item of unlocalized) console.error(`  ${item}`);
  }
  process.exitCode = 1;
} else {
  console.log(
    `i18n coverage OK: ${used.size} UI strings have English translations.`,
  );
}
