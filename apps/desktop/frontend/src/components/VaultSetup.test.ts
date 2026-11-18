import { mount, flushPromises } from '@vue/test-utils';
import { beforeEach, expect, it, vi } from 'vitest';
import VaultSetup from './VaultSetup.vue';
const service = vi.hoisted(() => ({
  SetupVault: vi.fn<(...args: unknown[]) => Promise<string>>(),
  UnlockVault: vi.fn<(...args: unknown[]) => Promise<void>>(),
  UnlockRememberedVault: vi.fn<() => Promise<void>>(),
  ExportVaultRecovery: vi.fn<(...args: unknown[]) => Promise<boolean>>(),
  SetPrivateMode: vi.fn<(...args: unknown[]) => Promise<void>>(),
}));
vi.mock('../../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  TransferService: service,
}));
const button = (w: ReturnType<typeof mount>, text: string) =>
  w.findAll('button').find((b) => b.text().includes(text))!;
beforeEach(() => vi.clearAllMocks());

it('creates a private vault only after confirmation and a saved recovery file', async () => {
  service.SetupVault.mockResolvedValue('recovery secret');
  service.ExportVaultRecovery.mockResolvedValueOnce(false).mockResolvedValue(true);
  const w = mount(VaultSetup, { props: { mode: 'choose' } });
  await w.get('input[value="private"]').setValue(true);
  await w.get('form').trigger('submit');
  expect(w.text()).toContain('Create your vault passphrase');
  const [pass, confirm] = w.findAll('input[type="password"]');
  await pass!.setValue('separate vault passphrase');
  await confirm!.setValue('different');
  expect(w.text()).toContain('do not match');
  await confirm!.setValue('separate vault passphrase');
  expect(button(w, 'Create vault').attributes('disabled')).toBeDefined();
  await w.get('input[type="checkbox"]').setValue(true);
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(service.SetupVault).toHaveBeenCalledWith('separate vault passphrase', true);
  expect(service.SetPrivateMode).toHaveBeenCalledWith(true);
  expect(button(w, 'Continue to files').attributes('disabled')).toBeDefined();
  await button(w, 'Save recovery file').trigger('click');
  await flushPromises();
  expect(button(w, 'Continue to files').attributes('disabled')).toBeDefined();
  await button(w, 'Save recovery file').trigger('click');
  await flushPromises();
  expect(service.ExportVaultRecovery).toHaveBeenLastCalledWith('recovery secret');
  await button(w, 'Continue to files').trigger('click');
  expect(w.emitted('done')).toHaveLength(1);
  w.unmount();
});

it('keeps standard storage one step and clears secrets after a rejected unlock', async () => {
  const standard = mount(VaultSetup, { props: { mode: 'choose' } });
  await standard.get('form').trigger('submit');
  await flushPromises();
  expect(service.SetPrivateMode).toHaveBeenCalledWith(false);
  expect(standard.emitted('done')).toHaveLength(1);
  service.UnlockVault.mockRejectedValue(new Error('vault unlock failed'));
  const w = mount(VaultSetup, { props: { mode: 'unlock' } });
  await w.get('input[type="password"]').setValue('wrong password');
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(w.get('[role="alert"]').text()).toContain('vault unlock failed');
  expect(w.get<HTMLInputElement>('input[type="password"]').element.value).toBe('');
  expect(service.SetPrivateMode).toHaveBeenCalledTimes(1);
  expect(w.emitted('done')).toBeUndefined();
});
