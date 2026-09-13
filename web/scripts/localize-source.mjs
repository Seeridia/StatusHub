import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

const sourceRoot = path.resolve("src");
const files = [];
function collect(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) collect(target);
    else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name) && entry.name !== "i18n.ts") files.push(target);
  }
}
collect(sourceRoot);

const hasHan = (value) => /[\u3400-\u9fff]/.test(value);
const factory = ts.factory;

for (const filename of files) {
  const original = fs.readFileSync(filename, "utf8");
  if (!hasHan(original)) continue;
  const kind = filename.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const source = ts.createSourceFile(filename, original, ts.ScriptTarget.Latest, true, kind);
  let changed = false;
  const trCall = (key, options) => factory.createCallExpression(
    factory.createIdentifier("tr"), undefined,
    options ? [factory.createStringLiteral(key), options] : [factory.createStringLiteral(key)],
  );
  const transformer = (context) => {
    const visit = (node) => {
      if (ts.isJsxText(node) && hasHan(node.text)) {
        const key = node.text.trim().replace(/\s+/g, " ");
        if (!key) return node;
        changed = true;
        return factory.createJsxExpression(undefined, trCall(key));
      }
      if (ts.isJsxAttribute(node) && node.initializer && ts.isStringLiteral(node.initializer) && hasHan(node.initializer.text)) {
        changed = true;
        return factory.updateJsxAttribute(node, node.name, factory.createJsxExpression(undefined, trCall(node.initializer.text)));
      }
      if (ts.isTemplateExpression(node) && hasHan(node.getText(source))) {
        changed = true;
        let key = node.head.text;
        const properties = [];
        node.templateSpans.forEach((span, index) => {
          const name = `value${index}`;
          key += `{{${name}}}` + span.literal.text;
          properties.push(factory.createPropertyAssignment(name, ts.visitNode(span.expression, visit)));
        });
        return trCall(key, factory.createObjectLiteralExpression(properties, false));
      }
      if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && hasHan(node.text)) {
        changed = true;
        return trCall(node.text);
      }
      return ts.visitEachChild(node, visit, context);
    };
    return (root) => ts.visitNode(root, visit);
  };
  const transformed = ts.transform(source, [transformer]).transformed[0];
  if (!changed) continue;
  let relative = path.relative(path.dirname(filename), path.join(sourceRoot, "lib/i18n")).replaceAll(path.sep, "/");
  if (!relative.startsWith(".")) relative = `./${relative}`;
  const importNode = factory.createImportDeclaration(
    undefined,
    factory.createImportClause(false, undefined, factory.createNamedImports([
      factory.createImportSpecifier(false, undefined, factory.createIdentifier("tr")),
    ])),
    factory.createStringLiteral(relative),
  );
  const updated = factory.updateSourceFile(transformed, [importNode, ...transformed.statements]);
  fs.writeFileSync(filename, ts.createPrinter({ newLine: ts.NewLineKind.LineFeed }).printFile(updated));
  console.log(path.relative(process.cwd(), filename));
}
