import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Accounts from './Accounts.vue';

afterEach(() => vi.unstubAllGlobals());
const reply = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
it('signs in an administrator, creates a user, and clears the one-time password on sign-out', async () => {
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', request);
  request
    .mockResolvedValueOnce(reply({ challenge: 'challenge', next_step: 'second_factor' }))
    .mockResolvedValueOnce(reply({ role: 'admin', access_token: 'session' }))
    .mockResolvedValueOnce(
      reply({ username: 'alice', temporary_password: 'ONE-TIME', expires_at: 'tomorrow' }),
    )
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  const wrapper = mount(Accounts);
  const inputs = wrapper.findAll('input');
  await inputs[0].setValue('admin');
  await inputs[1].setValue('password');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  await wrapper.get('input').setValue('123456');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('Create user account');
  await wrapper.get('input').setValue('alice');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(request.mock.calls[2][0]).toBe('/api/v1/admin/users');
  expect(request.mock.calls[2][1]?.headers).toMatchObject({ Authorization: 'Bearer session' });
  expect(wrapper.text()).toContain('ONE-TIME');
  await wrapper.findAll('button').at(-1)!.trigger('click');
  await flushPromises();
  expect(wrapper.text()).not.toContain('ONE-TIME');
  expect(wrapper.text()).toContain('Administrator sign-in');
  wrapper.unmount();
});
it('rejects user accounts in the admin interface and ends the issued session', async () => {
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', request);
  request
    .mockResolvedValueOnce(reply({ challenge: 'challenge', next_step: 'second_factor' }))
    .mockResolvedValueOnce(reply({ role: 'user', access_token: 'user-session' }))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  const wrapper = mount(Accounts);
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  await wrapper.get('input').setValue('123456');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('Use the desktop app');
  expect(wrapper.text()).not.toContain('Create user account');
  expect(request.mock.calls[2][1]?.method).toBe('DELETE');
  wrapper.unmount();
});
