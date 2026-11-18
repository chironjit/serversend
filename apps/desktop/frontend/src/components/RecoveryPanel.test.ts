import { mount, flushPromises } from '@vue/test-utils';
import { expect, it, vi } from 'vitest';
import RecoveryPanel from './RecoveryPanel.vue';
const service = vi.hoisted(() => ({
  TrashPage: vi.fn<(...args: string[]) => Promise<any>>(),
  RestoreTrash: vi.fn<(...args: string[]) => Promise<any>>(),
  PurgeTrash: vi.fn<(...args: string[]) => Promise<any>>(),
  History: vi.fn<(...args: string[]) => Promise<any>>(),
  SelectVersion: vi.fn<(...args: string[]) => Promise<any>>(),
  ChooseAndDownloadVersion: vi.fn<(...args: string[]) => Promise<any>>(),
}));
vi.mock('../../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  TransferService: service,
}));
it('requires explicit purge confirmation and keeps server errors visible', async () => {
  service.TrashPage.mockResolvedValue({ entries: [{ id: 'one', standard: { name: 'report' } }] });
  service.PurgeTrash.mockRejectedValueOnce(new Error('Downloads are active'));
  const w = mount(RecoveryPanel);
  await flushPromises();
  const button = (text: string) => w.findAll('button').find((b) => b.text() === text)!;
  await button('Purge…').trigger('click');
  expect(service.PurgeTrash).not.toHaveBeenCalled();
  await button('Purge permanently').trigger('click');
  await flushPromises();
  expect(w.get('[role="alert"]').text()).toContain('Downloads are active');
  expect(service.PurgeTrash).toHaveBeenCalledWith('one');
  w.unmount();
});
it('downloads and selects immutable versions and replaces pages', async () => {
  service.History.mockResolvedValueOnce({
    versions: [{ id: 'old', created_at: '2026-11-18', transmitted_bytes: 4, conflicted: true }],
    next_cursor: 'next',
  }).mockResolvedValue({ versions: [] });
  const w = mount(RecoveryPanel, { props: { fileId: 'file', name: 'report' } });
  await flushPromises();
  expect(w.text()).toContain('Conflict');
  await w
    .findAll('button')
    .find((b) => b.text() === 'Download version')!
    .trigger('click');
  await flushPromises();
  expect(service.ChooseAndDownloadVersion).toHaveBeenCalledWith('file', 'old');
  expect(w.text()).not.toContain('4 bytes');
  w.unmount();
});
