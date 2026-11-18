// Real Go process and transport, using only Node built-ins. No browser download.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';

const reservation = createServer();
reservation.listen(0, '127.0.0.1');
await once(reservation, 'listening');
const port = reservation.address().port;
await new Promise((resolve) => reservation.close(resolve));
const origin = `http://127.0.0.1:${port}`;
const child = spawn(process.argv[2], [], {
  env: { ...process.env, WAILS_SERVER_HOST: '127.0.0.1', WAILS_SERVER_PORT: String(port) },
  stdio: ['ignore', 'pipe', 'pipe'],
});
const exited = once(child, 'exit');
let logs = '';
child.stdout.on('data', (data) => {
  logs += data;
});
child.stderr.on('data', (data) => {
  logs += data;
});
const request = (path, options = {}) =>
  fetch(origin + path, { ...options, signal: AbortSignal.timeout(2000) });
try {
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    assert.equal(child.exitCode, null, `Scaffold exited during startup: ${logs}`);
    try {
      const response = await request('/health');
      ready = response.ok && (await response.json()).status === 'ok';
    } catch {
      /* Listener has not started yet. */
    }
    if (ready) break;
    await delay(100);
  }
  assert.ok(ready, `Scaffold startup timed out: ${logs}`);
  const index = await (await request('/')).text();
  assert.match(index, /id="app"/);
  const assets = [...index.matchAll(/(?:src|href)="(\/[^"#?]+)"/g)];
  assert.ok(assets.some((match) => match[1].startsWith('/assets/')));
  for (const [, path] of assets) {
    const response = await request(path);
    assert.equal(response.status, 200, `Asset unavailable: ${path}`);
    assert.ok((await response.arrayBuffer()).byteLength > 0, `Empty asset: ${path}`);
  }

  const bindingID = async (file, name) => {
    const source = await readFile(
      new URL(
        `../frontend/bindings/github.com/chironjit/serversend/apps/desktop/${file}`,
        import.meta.url,
      ),
      'utf8',
    );
    const match = source.match(
      new RegExp(`export function ${name}\\([^)]*\\)[\\s\\S]*?\\$Call\\.ByID\\((\\d+)`),
    );
    assert.ok(match, `Generated ${name} binding missing`);
    return Number(match[1]);
  };
  const call = (methodID, args, extraHeaders = {}) =>
    request('/wails/runtime', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: origin, ...extraHeaders },
      body: JSON.stringify({
        object: 0,
        method: 0,
        args: { 'call-id': crypto.randomUUID(), methodID, args },
      }),
    });
  const setPaused = await bindingID('backgroundservice.ts', 'SetPaused');
  const paused = await bindingID('backgroundservice.ts', 'Paused');
  assert.equal((await call(setPaused, [true])).status, 200);
  assert.equal(await (await call(paused, [])).json(), true, 'Global pause was not applied');
  assert.ok(
    (await call(setPaused, ['yes'])).status >= 400,
    'Malformed bridge argument was accepted',
  );
  assert.equal(
    (await call(setPaused, [false], { Origin: 'https://untrusted.invalid' })).status,
    403,
  );
  assert.equal(await (await call(paused, [])).json(), true, 'Untrusted origin changed state');

  const batches = await call(await bindingID('transferservice.ts', 'Batches'), []);
  assert.equal(batches.status, 200);
  assert.deepEqual(
    await batches.json(),
    [],
    'New desktop process should have no upload selections',
  );

  const ws = new WebSocket(origin.replace('http:', 'ws:') + '/wails/events');
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Event connection timed out')), 5000);
      ws.addEventListener('open', () => {
        clearTimeout(timer);
        resolve();
      });
      ws.addEventListener('error', () => {
        clearTimeout(timer);
        reject(new Error('Event connection failed'));
      });
    });
  } finally {
    ws.close();
  }
  console.log(
    'Scaffold smoke passed: embedded assets, generated Go bindings, durable global pause, upload summaries, invalid input, origin guard, event connection',
  );
} finally {
  child.kill('SIGTERM');
  const timeout = setTimeout(() => child.kill('SIGKILL'), 5000);
  try {
    await exited;
  } finally {
    clearTimeout(timeout);
  }
}
