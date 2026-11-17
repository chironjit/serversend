import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import html from './update.html?raw';

type View = {
  revision: number;
  state: string;
  current: string;
  version?: string;
  notes?: string;
  message?: string;
};
const listeners = new Map<string, (event: { data: View }) => void>();
const emit = vi.fn<(name: string) => void>();
const get = (id: string) => document.getElementById(id)!;
const render = (view: View) => listeners.get('serversend:update:state')!({ data: view });

beforeEach(() => {
  emit.mockReset();
  listeners.clear();
  vi.stubGlobal('wails', {
    Events: {
      On: (name: string, callback: (event: { data: View }) => void) =>
        listeners.set(name, callback),
      Emit: emit,
    },
  });
  vi.stubGlobal('_wails', { invoke: vi.fn<(message: string) => void>() });
  const parsed = new DOMParser().parseFromString(html, 'text/html');
  document.body.innerHTML = parsed.body.innerHTML;
  // Execute the shipped inline script against the same event bridge it uses in Wails.
  window.eval(parsed.querySelector('script')!.textContent!);
});
afterEach(() => {
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

it('offers a release without emitting installation and renders release text safely', () => {
  render({
    revision: 1,
    state: 'available',
    current: '1.0.0',
    version: '2.0.0',
    notes: '<img src=x onerror=alert(1)>',
  });
  expect(get('version').textContent).toContain('1.0.0 → 2.0.0');
  expect(get('notes').querySelector('img')).toBeNull();
  expect(emit).toHaveBeenCalledExactlyOnceWith('serversend:update:ready');
  get('install').click();
  get('install').click();
  expect(emit).toHaveBeenCalledTimes(2);
  expect(emit).toHaveBeenLastCalledWith('serversend:update:install');
});

it('postpones the update and prevents late state events from re-enabling consent', () => {
  render({ revision: 1, state: 'available', current: '1.0.0', version: '2.0.0' });
  get('close').click();
  expect(emit).toHaveBeenLastCalledWith('serversend:update:close');
  render({ revision: 2, state: 'installing', current: '1.0.0', version: '2.0.0' });
  render({ revision: 1, state: 'available', current: '1.0.0', version: '2.0.0' });
  expect((get('close') as HTMLButtonElement).disabled).toBe(true);
  expect(get('install').hidden).toBe(true);
});

it('shows failures and permits an explicit retry', () => {
  render({
    revision: 3,
    state: 'error',
    current: '1.0.0',
    version: '2.0.0',
    message: 'Checksum mismatch',
  });
  expect(get('message').textContent).toBe('Checksum mismatch');
  expect((get('install') as HTMLButtonElement).disabled).toBe(false);
  get('install').click();
  expect(emit).toHaveBeenLastCalledWith('serversend:update:install');
});
