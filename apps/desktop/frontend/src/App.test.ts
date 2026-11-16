import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import App from './App.vue';

type Listener = (event: { data: unknown }) => void;
const bridge = vi.hoisted(() => ({
  listener: undefined as Listener | undefined,
  service: {
    Probe: vi.fn<(address: string) => Promise<unknown>>(),
    Approve: vi.fn<(fingerprint: string) => Promise<void>>(),
    SignIn: vi.fn<(username: string, password: string) => Promise<{ next: string }>>(),
    ReplacePassword: vi.fn<(password: string) => Promise<{ next: string }>>(),
    BeginEnrollment: vi.fn<() => Promise<{ secret: string; qr: string }>>(),
    ConfirmEnrollment: vi.fn<(code: string) => Promise<string[] | null>>(),
    CompleteSignIn: vi.fn<(code: string) => Promise<unknown>>(),
    SignOut: vi.fn<() => Promise<void>>(),
    Files: vi.fn<() => Promise<{ id: string; name: string }[] | null>>(),
    ChooseAndUpload: vi.fn<() => Promise<number>>(),
    ChooseAndDownload: vi.fn<(id: string) => Promise<boolean>>(),
  },
}));
vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (_name: string, listener: Listener) => {
      bridge.listener = listener;
      return () => undefined;
    },
  },
}));
vi.mock('../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  TransferService: bridge.service,
}));

const fingerprint = 'ab12'.repeat(16);

async function submit(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('form').trigger('submit');
  await flushPromises();
}

describe('desktop transfer slice', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    delete document.documentElement.dataset.theme;
    bridge.service.Probe.mockResolvedValue({
      origin: 'https://server.test:58443',
      fingerprint,
      subject: 'CN=server',
      publicly_trusted: false,
    });
    bridge.service.Approve.mockResolvedValue();
    bridge.service.Files.mockResolvedValue([{ id: 'f1', name: 'report.txt' }]);
    bridge.service.ChooseAndUpload.mockResolvedValue(1);
    bridge.service.ChooseAndDownload.mockResolvedValue(true);
    bridge.service.SignOut.mockResolvedValue();
  });

  it('onboards a new account through every mandatory step before showing files', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'replace_password' });
    bridge.service.ReplacePassword.mockResolvedValue({ next: 'enroll_totp' });
    bridge.service.BeginEnrollment.mockResolvedValue({
      secret: 'SECRETKEY',
      qr: 'data:image/png;base64,AA==',
    });
    bridge.service.ConfirmEnrollment.mockResolvedValue(['AAAA-1111', 'BBBB-2222']);
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'admin',
      origin: 'https://server.test:58443',
      serverId: 's',
    });
    const wrapper = mount(App);

    await wrapper.find('input').setValue('server.test:58443');
    await submit(wrapper);
    expect(wrapper.find('[data-test="fingerprint"]').text()).toBe(
      fingerprint.match(/.{4}/g)?.join(' '),
    );
    expect(wrapper.text()).toContain('private certificate');
    expect(bridge.service.Approve).not.toHaveBeenCalled();

    await wrapper.find('button').trigger('click');
    await flushPromises();
    expect(bridge.service.Approve).toHaveBeenCalledWith(fingerprint);

    const [user, pass] = wrapper.findAll('input');
    await user?.setValue('admin');
    await pass?.setValue('temporary');
    await submit(wrapper);
    expect(wrapper.text()).toContain('Choose your password');

    await wrapper.find('input').setValue('a long permanent password');
    await submit(wrapper);
    expect(wrapper.find('img').attributes('src')).toBe('data:image/png;base64,AA==');
    expect(wrapper.text()).toContain('SECRETKEY');

    await wrapper.find('input').setValue('123456');
    await submit(wrapper);
    expect(wrapper.text()).toContain('AAAA-1111');
    expect(wrapper.text()).not.toContain('SECRETKEY');
    expect(bridge.service.Files).not.toHaveBeenCalled();

    await wrapper.find('button').trigger('click');
    await wrapper.find('input').setValue('654321');
    await submit(wrapper);
    expect(bridge.service.CompleteSignIn).toHaveBeenCalledWith('654321');
    expect(wrapper.text()).toContain('report.txt');
    expect(wrapper.text()).not.toContain('AAAA-1111');
  });

  it('keeps the user on the same step with the reason when an action fails', async () => {
    bridge.service.Probe.mockRejectedValue(new Error('could not reach a ServerSend server'));
    const wrapper = mount(App);
    await wrapper.find('input').setValue('nowhere.test');
    await submit(wrapper);
    expect(wrapper.find('[role="alert"]').text()).toContain('could not reach');
    expect(wrapper.text()).toContain('Connect to a server');
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined();
  });

  it('shows live transfer progress, failures and refreshed files', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'admin',
      origin: 'https://server.test:58443',
      serverId: 's',
    });
    const wrapper = mount(App);
    await wrapper.find('input').setValue('server.test:58443');
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await wrapper.find('input').setValue('123456');
    await submit(wrapper);
    expect(wrapper.text()).toContain('Uploads and downloads from this session appear here.');

    const emit = (item: object) => bridge.listener?.({ data: item });
    emit({
      id: 1,
      name: 'video.mp4',
      direction: 'upload',
      state: 'active',
      done: 524288,
      total: 1048576,
      error: '',
    });
    await flushPromises();
    expect(wrapper.find('progress').attributes('value')).toBe('50');
    expect(wrapper.text()).toContain('512.0 KiB of 1.0 MiB');

    bridge.service.Files.mockResolvedValue([
      { id: 'f1', name: 'report.txt' },
      { id: 'f2', name: 'video.mp4' },
    ]);
    emit({
      id: 1,
      name: 'video.mp4',
      direction: 'upload',
      state: 'done',
      done: 1048576,
      total: 1048576,
      error: '',
    });
    emit({
      id: 2,
      name: 'gone.bin',
      direction: 'download',
      state: 'failed',
      done: 0,
      total: 0,
      error: 'Sign in again to continue',
    });
    await flushPromises();
    expect(wrapper.find('progress').exists()).toBe(false);
    expect(wrapper.text()).toContain('Done · 1.0 MiB');
    expect(wrapper.text()).toContain('Sign in again to continue');
    expect(wrapper.findAll('.list')[0]?.text()).toContain('video.mp4');
  });

  it('applies and persists the chosen theme', async () => {
    const wrapper = mount(App);
    expect(document.documentElement.dataset.theme).toBeUndefined();
    await wrapper.find('select').setValue('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(localStorage.getItem('serversend-theme')).toBe('dark');
    wrapper.unmount();
    mount(App);
    expect(document.documentElement.dataset.theme).toBe('dark');
  });
});
