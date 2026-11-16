// Vite can emit unprocessed Tailwind directives with warnings and exit zero.
// Refuse that output so an incomplete integration never appears deployable.
import { readFileSync, readdirSync } from 'node:fs';

const styles = readdirSync('dist/assets').filter((name) => name.endsWith('.css'));
if (styles.length === 0) throw new Error('Admin build produced no stylesheet');
for (const name of styles) {
  const css = readFileSync(`dist/assets/${name}`, 'utf8');
  if (/@(?:apply|tailwind|custom-variant|theme)\b/.test(css)) {
    throw new Error(
      'Admin CSS is not compiled. Configure the approved Tailwind build integration and rebuild.',
    );
  }
}
