#!/usr/bin/env node
/**
 * Write dist/manifest.xml with every placeholder URL pointed at the deployed
 * origin, so one manifest.xml in git serves both local development and every
 * deployment.
 *
 *   node scripts/make-manifest.mjs --origin https://addin.yourfirm.com [--out dist/manifest.xml] [--id <guid>]
 *
 * --id replaces the add-in Id; use it for a second, independent deployment so
 * the two never collide in a tenant's add-in catalog.
 */
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..');

export const PLACEHOLDER_ORIGIN = 'https://localhost:3200';

export function parseArgs(argv) {
  const out = { origin: '', out: 'dist/manifest.xml', id: '' };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = () => {
      i += 1;
      return argv[i];
    };
    if (arg === '--origin') out.origin = next() ?? '';
    else if (arg === '--out') out.out = next() ?? out.out;
    else if (arg === '--id') out.id = next() ?? '';
    else if (arg.startsWith('--origin=')) out.origin = arg.slice('--origin='.length);
    else if (arg.startsWith('--out=')) out.out = arg.slice('--out='.length);
    else if (arg.startsWith('--id=')) out.id = arg.slice('--id='.length);
  }
  return out;
}

export function normalizeOrigin(raw) {
  const value = String(raw || '').trim().replace(/\/+$/, '');
  if (!/^https:\/\/[^/\s]+$/.test(value)) {
    throw new Error(`--origin must be an https origin such as https://addin.yourfirm.com, got ${JSON.stringify(raw)}`);
  }
  return value;
}

export function rewriteManifest(xml, origin, id = '') {
  let out = xml.split(PLACEHOLDER_ORIGIN).join(origin);
  if (id) {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) {
      throw new Error(`--id must be a GUID, got ${JSON.stringify(id)}`);
    }
    out = out.replace(/<Id>[^<]*<\/Id>/, `<Id>${id}</Id>`);
  }
  return out;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const origin = normalizeOrigin(args.origin);
  const source = readFileSync(resolve(root, 'manifest.xml'), 'utf8');
  const target = resolve(root, args.out);
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, rewriteManifest(source, origin, args.id), 'utf8');
  console.warn(`Wrote ${target} for ${origin}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main();
}
