import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import App from './App.vue';

describe('public admin shell', () => {
  let wrapper: VueWrapper | undefined;
  const request = vi.fn<typeof fetch>();

  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addEventListener: vi.fn<() => void>(),
      removeEventListener: vi.fn<() => void>(),
    }));
    vi.stubGlobal('fetch', request);
    request.mockReset();
    request.mockResolvedValue(new Response(JSON.stringify({ status: 'ready' })));
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it.each(['light', 'dark'])(
    'renders a bounded public readiness check in %s mode',
    async (theme) => {
      localStorage.setItem('serversend.admin.theme', theme);
      wrapper = mount(App);
      expect(wrapper.text()).toContain('Checking server…');
      expect(wrapper.get('button').attributes('disabled')).toBeDefined();
      expect(wrapper.get('main').attributes('id')).toBe('main');
      expect(wrapper.get('a').attributes('href')).toBe('#main');
      expect(wrapper.find('form').exists()).toBe(true);
      expect(wrapper.find('input[type="password"]').exists()).toBe(true);
      await flushPromises();
      expect(wrapper.text()).toContain('Server is responding');
      expect(wrapper.text()).toContain('Administrator sign-in');
      expect(document.documentElement.dataset.theme).toBe(theme);
      expect(request).toHaveBeenCalledExactlyOnceWith(
        '/readyz',
        expect.objectContaining({
          credentials: 'omit',
          redirect: 'error',
          cache: 'no-store',
          signal: expect.any(AbortSignal),
        }),
      );
    },
  );

  it.each(['light', 'dark'])('handles failures safely and recovers in %s mode', async (theme) => {
    localStorage.setItem('serversend.admin.theme', theme);
    request.mockRejectedValueOnce(new Error('private diagnostic or credential'));
    wrapper = mount(App);
    await flushPromises();
    expect(wrapper.text()).toContain('Server is unavailable');
    expect(wrapper.text()).toContain('Check your network connection');
    expect(wrapper.text()).not.toContain('private diagnostic');
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Server is responding');
  });

  it.each([
    new Response('{"status":"unavailable"}'),
    new Response('{"status":"ready"}', { status: 503 }),
    new Response('<html>proxy error</html>'),
    new Response('null'),
    new Response('{}'),
  ])('treats unsuccessful or malformed readiness responses as unavailable', async (response) => {
    request.mockResolvedValueOnce(response);
    wrapper = mount(App);
    await flushPromises();
    expect(wrapper.text()).toContain('Server is unavailable');
    expect(wrapper.text()).not.toContain('proxy error');
  });

  it('bounds stalled requests, prevents overlapping probes, and retries after timeout', async () => {
    vi.useFakeTimers();
    let aborted = false;
    request.mockImplementationOnce(
      (_url, options) =>
        new Promise((_resolve, reject) => {
          options?.signal?.addEventListener('abort', () => {
            aborted = true;
            reject(new Error('aborted'));
          });
        }),
    );
    wrapper = mount(App);
    await wrapper.get('button').trigger('click');
    expect(request).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(5000);
    expect(aborted).toBe(true);
    expect(wrapper.text()).toContain('Server is unavailable');
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(request).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain('Server is responding');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('cancels an outstanding probe when the view is disposed', async () => {
    request.mockImplementationOnce(
      (_url, options) =>
        new Promise((_resolve, reject) => {
          options?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
        }),
    );
    wrapper = mount(App);
    const signal = request.mock.calls[0][1]?.signal;
    wrapper.unmount();
    wrapper = undefined;
    expect(signal?.aborted).toBe(true);
    await flushPromises();
  });
});
