import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const lock = JSON.parse(readFileSync(resolve(root, 'package-lock.json'), 'utf8'));
const notices = ['Atlas Schema Desk — bundled browser dependencies\n'];
for (const [path, entry] of Object.entries(lock.packages)) {
  if (!path || entry.dev || entry.devOptional) continue;
  const directory = resolve(root, path);
  const manifest = JSON.parse(readFileSync(resolve(directory, 'package.json'), 'utf8'));
  const files = readdirSync(directory).filter(name => /^(licen[cs]e|copying)(\..*)?$/i.test(name));
  notices.push(`\n${manifest.name} ${entry.version}\nLicense: ${manifest.license || entry.license || 'See upstream'}\n`);
  if (!files.length) {
    const readme = readdirSync(directory).find(name => /^readme(?:\..*)?$/i.test(name));
    const notice = readme && readFileSync(resolve(directory, readme), 'utf8').match(/(?:^|\n)#{0,3}\s*licen[cs]e[^\n]*\n[\s\S]*$/im)?.[0];
    if (!notice) throw new Error(`Missing license notice for bundled dependency ${manifest.name}`);
    notices.push(notice);
  }
  for (const file of files) notices.push(readFileSync(resolve(directory, file), 'utf8'));
}
writeFileSync(resolve(root, '../web/dist/app/THIRD_PARTY_LICENSES.txt'), notices.join('\n'));
