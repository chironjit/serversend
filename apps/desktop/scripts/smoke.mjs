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

  const binding = await readFile(
    new URL(
      '../frontend/bindings/github.com/chironjit/serversend/apps/desktop/greetservice.ts',
      import.meta.url,
    ),
    'utf8',
  );
  const method = binding.match(/\$Call\.ByID\((\d+),/);
  assert.ok(method, 'Generated greeting binding missing');
  const call = (args, extraHeaders = {}) =>
    request('/wails/runtime', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: origin, ...extraHeaders },
      body: JSON.stringify({
        object: 0,
        method: 0,
        args: { 'call-id': crypto.randomUUID(), methodID: Number(method[1]), args },
      }),
    });
  const greeting = await call(['Zoë 東京']);
  assert.equal(greeting.status, 200);
  assert.equal(await greeting.text(), 'Hello Zoë 東京!');
  assert.ok((await call([123])).status >= 400, 'Malformed bridge argument was accepted');
  assert.equal((await call(['Ada'], { Origin: 'https://untrusted.invalid' })).status, 403);

  const ws = new WebSocket(origin.replace('http:', 'ws:') + '/wails/events');
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('No Go time event received')), 5000);
      ws.addEventListener('message', (event) => {
        const message = JSON.parse(String(event.data));
        if (message.name === 'time' && typeof message.data === 'string') {
          clearTimeout(timer);
          resolve();
        }
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
    'Scaffold smoke passed: embedded assets, generated Go binding, invalid input, origin guard, live events',
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
