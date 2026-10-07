import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const target = resolve(frontend, '../../internal/managementasset/console');
const html = readFileSync(join(frontend, 'dist/index.html'));
if (!html.includes('<html') || !html.includes('</html>')) throw new Error('Missing production console');
const sources = createHash('sha256');
function include(path) {
  const entries = readdirSync(join(frontend, path), { withFileTypes: true });
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const relative = join(path, entry.name);
    if (entry.isDirectory()) include(relative);
    else { sources.update(relative); sources.update(readFileSync(join(frontend, relative))); }
  }
}
include('src');
for (const path of ['package.json', 'bun.lock', 'vite.config.ts', 'index.html', 'tsconfig.json', 'tsconfig.node.json']) {
  sources.update(path); sources.update(readFileSync(join(frontend, path)));
}
mkdirSync(target, { recursive: true });
writeFileSync(join(target, 'management.html.gz'), gzipSync(html, { level: 9 }));
writeFileSync(join(target, 'manifest.json'), `${JSON.stringify({
  source_sha256: sources.digest('hex'),
  html_sha256: createHash('sha256').update(html).digest('hex'),
  html_bytes: html.length,
}, null, 2)}\n`);
console.log(`Packaged the single console (${html.length} bytes) into the gateway binary.`);
