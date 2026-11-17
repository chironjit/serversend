import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Logs from './Logs.vue';
afterEach(() => vi.unstubAllGlobals());
it('loads authenticated logs, filters them and clears results on authorization failure', async () => {
  const request = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify([
          { id: 1, time: '2026-11-17T00:00:00Z', level: 'error', message: 'Disk failed' },
        ]),
      ),
    )
    .mockResolvedValueOnce(new Response('[]'))
    .mockResolvedValueOnce(new Response('{}', { status: 401 }));
  vi.stubGlobal('fetch', request);
  const wrapper = mount(Logs, { props: { token: 'session' } });
  await flushPromises();
  expect(wrapper.text()).toContain('Disk failed');
  expect(new Headers(request.mock.calls[0]![1]?.headers).get('Authorization')).toBe(
    'Bearer session',
  );
  await wrapper.get('input').setValue('disk');
  await wrapper.get('select').setValue('error');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(request.mock.calls[1][0]).toContain('level=error&q=disk');
  expect(wrapper.text()).toContain('No matching log entries');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('Sign in as an administrator again');
  wrapper.unmount();
});
