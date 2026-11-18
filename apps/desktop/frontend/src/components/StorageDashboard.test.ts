import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import StorageDashboard from './StorageDashboard.vue';
const load = vi.hoisted(() => vi.fn<() => Promise<unknown>>());
vi.mock('../../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  TransferService: { StorageStatistics: load },
}));
afterEach(() => {
  vi.useRealTimers();
  load.mockReset();
});
it('shows separate allocation and reservation totals and preserves stale values offline', async () => {
  vi.useFakeTimers();
  load
    .mockResolvedValueOnce({
      logical_bytes: 100,
      quota_bytes: 0,
      committed_bytes: 4096,
      retained_bytes: 4096,
      staged_bytes: 4096,
      reserved_bytes: 100,
      unwritten_bytes: 50,
      active_uploads: 1,
      updated_at: '2026-11-18T00:00:00Z',
      transfer: { useful_rate: 10, wire_rate: 20, retry_rate: 10 },
    })
    .mockRejectedValue(new Error('offline'));
  const wrapper = mount(StorageDashboard);
  await flushPromises();
  expect(wrapper.text()).toContain('Unlimited');
  expect(wrapper.text()).toContain('Reserved');
  expect(wrapper.text()).toContain('2026-11-18');
  await vi.advanceTimersByTimeAsync(5000);
  await flushPromises();
  expect(wrapper.text()).toContain('values are stale');
  expect(wrapper.text()).toContain('2026-11-18');
  wrapper.unmount();
  expect(vi.getTimerCount()).toBe(0);
});
