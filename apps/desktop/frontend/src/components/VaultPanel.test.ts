import { mount, flushPromises } from '@vue/test-utils';
import { expect, it, vi } from 'vitest';
import VaultPanel from './VaultPanel.vue';
const service = vi.hoisted(() => ({
  VaultUnlocked: vi.fn<() => Promise<boolean>>(),
  PrivateMode: vi.fn<() => Promise<boolean>>(),
  SetupVault: vi.fn<(...args: unknown[]) => Promise<string>>(),
  UnlockVault: vi.fn<(...args: unknown[]) => Promise<void>>(),
  ExportVaultRecovery: vi.fn<(...args: unknown[]) => Promise<boolean>>(),
  LockVault: vi.fn<() => Promise<void>>(),
  SetPrivateMode: vi.fn<(...args: unknown[]) => Promise<void>>(),
}));
vi.mock('../../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  TransferService: service,
}));
it('requires loss acknowledgement and preserves recovery export after a cancelled save', async () => {
  service.VaultUnlocked.mockResolvedValue(false);
  service.PrivateMode.mockResolvedValue(false);
  service.SetupVault.mockImplementation(async () => {
    service.VaultUnlocked.mockResolvedValue(true);
    return 'recovery secret';
  });
  service.ExportVaultRecovery.mockResolvedValue(false);
  const w = mount(VaultPanel);
  await flushPromises();
  const button = (text: string) => w.findAll('button').find((b) => b.text() === text)!;
  expect(button('Create vault').attributes('disabled')).toBeDefined();
  await w.get('input[type="password"]').setValue('separate vault passphrase');
  await w.findAll('input[type="checkbox"]')[1]!.setValue(true);
  await button('Create vault').trigger('click');
  await flushPromises();
  expect(service.SetupVault).toHaveBeenCalledWith('separate vault passphrase', true);
  expect(w.get<HTMLInputElement>('input[type="password"]').element.value).toBe('');
  await button('Export recovery package…').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('Export recovery package');
  await button('Lock and clear private views').trigger('click');
  await flushPromises();
  expect(w.emitted('locked')).toBeTruthy();
  expect(w.text()).not.toContain('Export recovery package');
  w.unmount();
});
it('clears secrets after a rejected unlock and leaves standard mode selectable', async () => {
  service.VaultUnlocked.mockResolvedValue(false);
  service.PrivateMode.mockResolvedValue(true);
  service.UnlockVault.mockRejectedValue(new Error('vault unlock failed'));
  const w = mount(VaultPanel);
  await flushPromises();
  await w.get('input[type="password"]').setValue('wrong password');
  await w
    .findAll('button')
    .find((b) => b.text() === 'Unlock')!
    .trigger('click');
  await flushPromises();
  expect(w.get('[role="alert"]').text()).toContain('vault unlock failed');
  expect(w.get<HTMLInputElement>('input[type="password"]').element.value).toBe('');
  await w.findAll('input[type="checkbox"]')[0]!.setValue(false);
  await flushPromises();
  expect(service.SetPrivateMode).toHaveBeenCalledWith(false);
  w.unmount();
});
