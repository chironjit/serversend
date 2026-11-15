// Repository and packaging references are checked without installing a parser.
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const read = (path) => readFileSync(resolve(root, path), 'utf8');
const exists = (path) => assert.ok(existsSync(resolve(root, path)), `Missing ${path}`);
for (const path of ['apps/desktop', 'apps/server', 'shared', 'docs']) exists(path);
const workspace = read('go.work');
for (const path of ['apps/desktop', 'apps/server', 'shared']) {
  assert.ok(workspace.includes(`./${path}`), `Workspace missing ${path}`);
  assert.match(
    read(`${path}/go.mod`),
    new RegExp(`^module github.com/chironjit/serversend/${path}\\s`),
  );
}

const desktop = 'apps/desktop/';
const tasks = read(`${desktop}Taskfile.yml`);
assert.match(tasks, /APP_NAME:\s*"desktop"/);
for (const match of tasks.matchAll(/^\s+\w+:\s+(\.\/build\/\S+\.yml)$/gm)) {
  exists(desktop + match[1]);
}
for (const path of [
  'build/config.yml',
  'build/appicon.png',
  'build/windows/icon.ico',
  'build/windows/wails.exe.manifest',
  'build/windows/info.json',
  'build/windows/nsis/project.nsi',
  'build/windows/nsis/wails_tools.nsh',
  'build/windows/msix/template.xml',
  'build/windows/msix/app_manifest.xml',
  'build/darwin/Info.plist',
  'build/darwin/Info.dev.plist',
  'build/darwin/icons.icns',
  'build/linux/desktop.desktop',
  'build/linux/nfpm/nfpm.yaml',
  'build/linux/nfpm/scripts/postinstall.sh',
  'build/linux/appimage/build.sh',
  'build/docker/Dockerfile.cross',
  'build/docker/Dockerfile.server',
  'frontend/public/wails.png',
  'frontend/public/vue.svg',
  'frontend/public/Inter-Medium.ttf',
])
  exists(desktop + path);

for (const name of ['Info.plist', 'Info.dev.plist']) {
  assert.match(
    read(`${desktop}build/darwin/${name}`),
    /<key>CFBundleExecutable<\/key>\s*<string>desktop<\/string>/,
  );
}
assert.match(read(`${desktop}build/linux/desktop.desktop`), /^Exec=desktop$/m);
assert.match(read(`${desktop}build/linux/nfpm/nfpm.yaml`), /src: "\.\/bin\/desktop"/);
assert.match(read(`${desktop}build/windows/Taskfile.yml`), /BIN_DIR}}\/{{\.APP_NAME}}\.exe/);
JSON.parse(read(`${desktop}build/windows/info.json`));
const frontend = JSON.parse(read(`${desktop}frontend/package.json`));
assert.equal(frontend.dependencies['@wailsio/runtime'], '3.0.0');
assert.match(read(`${desktop}go.mod`), /github.com\/wailsapp\/wails\/v3 v3\.0\.0\s/);
console.log('Workspace, scaffold assets, and desktop packaging references verified');
