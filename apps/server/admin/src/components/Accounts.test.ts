import { afterEach, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Accounts from './Accounts.vue';

afterEach(() => vi.unstubAllGlobals());
const reply = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
it('signs in an administrator, keeps multiple pending users, and clears passwords on sign-out', async () => {
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', request);
  request
    .mockResolvedValueOnce(reply({ challenge: 'challenge', next_step: 'second_factor' }))
    .mockResolvedValueOnce(reply({ role: 'admin', access_token: 'session' }))
    .mockResolvedValueOnce(reply([]))
    .mockResolvedValueOnce(
      reply({ username: 'alice', temporary_password: 'ONE-TIME', expires_at: 'tomorrow' }),
    )
    .mockResolvedValueOnce(
      reply([
        {
          id: 'a',
          username: 'alice',
          state: 'password_required',
          temporary_password: 'ONE-TIME',
          expires_at: '2099-01-01',
        },
      ]),
    )
    .mockResolvedValueOnce(reply({ username: 'bob' }))
    .mockResolvedValueOnce(
      reply([
        {
          id: 'a',
          username: 'alice',
          state: 'password_required',
          temporary_password: 'ONE-TIME',
          expires_at: '2099-01-01',
        },
        {
          id: 'b',
          username: 'bob',
          state: 'password_required',
          temporary_password: 'SECOND',
          expires_at: '2099-01-01',
        },
      ]),
    )
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  const wrapper = mount(Accounts, { global: { stubs: { Logs: true } } });
  const inputs = wrapper.findAll('input');
  await inputs[0].setValue('admin');
  await inputs[1].setValue('password');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  await wrapper.get('input').setValue('123456');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('User accounts');
  await wrapper.get('input').setValue('alice');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(request.mock.calls[2][0]).toBe('/api/v1/admin/users');
  expect(request.mock.calls[2][1]?.headers).toMatchObject({ Authorization: 'Bearer session' });
  expect(wrapper.text()).toContain('ONE-TIME');
  await wrapper.get('input').setValue('bob');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('ONE-TIME');
  expect(wrapper.text()).toContain('SECOND');
  expect(wrapper.findAll('tbody tr')).toHaveLength(2);
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
  const wrapper = mount(Accounts, { global: { stubs: { Logs: true } } });
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  await wrapper.get('input').setValue('123456');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('Use the desktop app');
  expect(wrapper.text()).not.toContain('User accounts');
  expect(request.mock.calls[2][1]?.method).toBe('DELETE');
  wrapper.unmount();
});

it('restores the browser cookie session after refresh and shows per-user storage', async () => {
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', request);
  request
    .mockResolvedValueOnce(reply({ role: 'admin' }))
    .mockResolvedValueOnce(
      reply([{ id: 'a', username: 'alice', state: 'active', disabled: false, storage_bytes: 128 }]),
    )
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  const wrapper = mount(Accounts, { props: { restore: true } });
  await flushPromises();
  expect(wrapper.text()).toContain('alice');
  expect(wrapper.text()).toContain('128 B');
  expect(request.mock.calls[1][1]?.headers).toEqual({});
  await wrapper.findAll('button').at(-1)!.trigger('click');
  await flushPromises();
  expect(request.mock.calls[2][0]).toBe('/api/v1/sessions/current');
  expect(request.mock.calls[2][1]?.method).toBe('DELETE');
  expect(wrapper.text()).toContain('Administrator sign-in');
  wrapper.unmount();
});

it('lists offline devices, confirms revocation, and blocks mixed recovery controls', async () => {
  const request = vi.fn<typeof fetch>();
  vi.stubGlobal('fetch', request);
  let recovery = '';
  const devices = [
    {
      id: 'device-one',
      name: 'Lost laptop',
      platform: 'linux',
      app_version: '0.1',
      signed_in_at: '2026-01-01',
      last_seen_at: '2026-01-02',
      status: 'authorized',
      recently_active: false,
    },
  ];
  request.mockImplementation(async (input, options) => {
    const path = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    if (path === '/api/v1/sessions/current') return reply({ role: 'admin' });
    if (path === '/api/v1/admin/users')
      return reply([
        {
          id: 'alice',
          username: 'alice',
          state: recovery ? 'password_required' : 'active',
          disabled: false,
          recovery_kind: recovery,
        },
      ]);
    if (path.endsWith('/revoke')) return new Response(null, { status: 204 });
    if (path.endsWith('/devices')) return reply(devices);
    if (path.endsWith('/reset-password') && options?.method === 'POST') {
      recovery = 'password';
      return reply({ temporary_password: 'RESET-ONCE', expires_at: '2099-01-01' });
    }
    throw new Error('Unexpected request ' + path);
  });
  const wrapper = mount(Accounts, { props: { restore: true } });
  await flushPromises();
  const button = (label: string) =>
    wrapper.findAll('button').find((value) => value.text() === label)!;
  await button('Manage access').trigger('click');
  await flushPromises();
  expect(wrapper.text()).toContain('Lost laptop');
  expect(wrapper.text()).toContain('Not recently seen');
  await button('Revoke access').trigger('click');
  expect(
    request.mock.calls.some(([path]) => typeof path === 'string' && path.endsWith('/revoke')),
  ).toBe(false);
  await button('Confirm').trigger('click');
  await flushPromises();
  expect(request).toHaveBeenCalledWith(
    '/api/v1/admin/users/alice/devices/device-one/revoke',
    expect.objectContaining({ method: 'POST' }),
  );
  await button('Reset password').trigger('click');
  await button('Confirm').trigger('click');
  await flushPromises();
  expect(wrapper.text()).toContain('RESET-ONCE');
  expect(button('Reset TOTP').attributes('disabled')).toBeDefined();
  await button('Dismiss password').trigger('click');
  expect(wrapper.text()).not.toContain('RESET-ONCE');
  wrapper.unmount();
});
