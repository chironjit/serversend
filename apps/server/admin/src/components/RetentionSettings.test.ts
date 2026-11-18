import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import RetentionSettings from './RetentionSettings.vue';
afterEach(() => vi.unstubAllGlobals());
it('loads saved policy and sends explicit settings with CSRF protection', async () => {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(new Response(JSON.stringify({ trash_days: 30, version_days: 0 })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ trash_days: 7, version_days: 90 })));
  vi.stubGlobal('fetch', fetcher);
  const w = mount(RetentionSettings, { props: { csrf: 'csrf-token' } });
  await flushPromises();
  await w.findAll('input')[0]!.setValue('7');
  await w.findAll('input')[1]!.setValue('90');
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(fetcher).toHaveBeenLastCalledWith(
    '/api/v1/admin/retention',
    expect.objectContaining({
      method: 'PUT',
      headers: expect.objectContaining({ 'X-CSRF-Token': 'csrf-token' }),
      body: '{"trash_days":7,"version_days":90}',
    }),
  );
  expect(w.get('[role="status"]').text()).toBe('Retention saved.');
  w.unmount();
});
