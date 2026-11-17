import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import App from './App.vue';

type Listener = (event: { data: unknown }) => void;
const bridge = vi.hoisted(() => ({
  listener: undefined as Listener | undefined,
  service: {
    Devices: vi.fn<(before: string) => Promise<unknown[]>>(),
    RevokeDevice: vi.fn<(id: string) => Promise<void>>(),
    ChooseServerCA: vi.fn<(address: string) => Promise<unknown>>(),
    SessionStatus: vi.fn<() => Promise<{ next: string }>>(),
    CompleteReauthentication: vi.fn<(code: string) => Promise<unknown>>(),
    Profiles: vi.fn<() => Promise<unknown[]>>(),
    SetRemember: vi.fn<(remember: boolean) => Promise<void>>(),
    OpenProfile: vi.fn<(origin: string) => Promise<unknown>>(),
    RemoveProfile: vi.fn<(origin: string) => Promise<void>>(),
    Probe: vi.fn<(address: string) => Promise<unknown>>(),
    Approve: vi.fn<(fingerprint: string) => Promise<void>>(),
    SignIn: vi.fn<(username: string, password: string) => Promise<{ next: string }>>(),
    ReplacePassword: vi.fn<(password: string) => Promise<{ next: string }>>(),
    BeginEnrollment: vi.fn<() => Promise<{ secret: string; qr: string }>>(),
    ConfirmEnrollment: vi.fn<(code: string) => Promise<string[] | null>>(),
    CompleteSignIn: vi.fn<(code: string) => Promise<unknown>>(),
    SignOut: vi.fn<() => Promise<void>>(),
    Batches: vi.fn<() => Promise<unknown[] | null>>(),
    Files:
      vi.fn<
        (parent: string) => Promise<{ id: string; name: string; directory?: boolean }[] | null>
      >(),
    DeleteEntry: vi.fn<(id: string) => Promise<void>>(),
    ChooseAndUploadFolder: vi.fn<(parent: string) => Promise<void>>(),
    CancelTransfer: vi.fn<(id: number) => Promise<void>>(),
    CancelBatch: vi.fn<(id: number) => Promise<void>>(),
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
  it('restores a saved server session without password or certificate approval', async () => {
    bridge.service.Profiles.mockResolvedValue([
      { origin: 'https://saved.test:58443', username: 'alice', fingerprint, remember: true },
    ]);
    bridge.service.OpenProfile.mockResolvedValue({
      username: 'alice',
      origin: 'https://saved.test:58443',
      serverId: 's1',
    });
    const wrapper = mount(App);
    await flushPromises();
    const saved = wrapper.findAll('button').find((button) => button.text().includes('saved.test'))!;
    await saved.trigger('click');
    await flushPromises();
    expect(bridge.service.OpenProfile).toHaveBeenCalledWith('https://saved.test:58443');
    expect(bridge.service.SignIn).not.toHaveBeenCalled();
    expect(bridge.service.Approve).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('report.txt');
    expect(wrapper.text()).toContain('Switch server');
    wrapper.unmount();
  });
  it('lists this device and offline devices and confirms individual or other-device revocation', async () => {
    bridge.service.Profiles.mockResolvedValue([
      { origin: 'https://saved.test:58443', username: 'alice', remember: true },
    ]);
    bridge.service.OpenProfile.mockResolvedValue({
      username: 'alice',
      origin: 'https://saved.test:58443',
      serverId: 's1',
    });
    bridge.service.Devices.mockResolvedValue([
      {
        id: 'current',
        name: 'Current laptop',
        current: true,
        status: 'authorized',
        last_seen_at: '2026-01-01',
      },
      {
        id: 'lost',
        name: 'Lost laptop',
        current: false,
        status: 'authorized',
        recently_active: false,
        last_seen_at: '2025-12-01',
      },
    ]);
    bridge.service.RevokeDevice.mockResolvedValue();
    const wrapper = mount(App);
    await flushPromises();
    const button = (text: string) => wrapper.findAll('button').find((b) => b.text() === text)!;
    await wrapper
      .findAll('button')
      .find((b) => b.text().includes('saved.test'))!
      .trigger('click');
    await flushPromises();
    await button('Devices').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Lost laptop');
    expect(wrapper.text()).toContain('This device');
    await button('Revoke access').trigger('click');
    await flushPromises();
    expect(bridge.service.RevokeDevice).not.toHaveBeenCalled();
    await button('Confirm revocation').trigger('click');
    await flushPromises();
    expect(bridge.service.RevokeDevice).toHaveBeenCalledWith('lost');
    await button('Sign out other devices').trigger('click');
    await flushPromises();
    await button('Confirm revocation').trigger('click');
    await flushPromises();
    expect(bridge.service.RevokeDevice).toHaveBeenCalledWith('others');
    bridge.service.RevokeDevice.mockRejectedValueOnce(
      new Error('Device access could not be revoked'),
    );
    await button('Revoke access').trigger('click');
    await flushPromises();
    await button('Confirm revocation').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Device access could not be revoked');
    wrapper.unmount();
  });
  it('renews a remembered device with TOTP without asking for a password', async () => {
    bridge.service.Profiles.mockResolvedValue([
      { origin: 'https://saved.test:58443', username: 'alice', fingerprint, remember: true },
    ]);
    bridge.service.OpenProfile.mockResolvedValue({
      username: 'alice',
      origin: 'https://saved.test:58443',
      serverId: 's1',
      reauthenticationRequired: true,
    });
    bridge.service.CompleteReauthentication.mockResolvedValue({
      username: 'alice',
      origin: 'https://saved.test:58443',
      serverId: 's1',
    });
    const wrapper = mount(App);
    await flushPromises();
    await wrapper
      .findAll('button')
      .find((button) => button.text().includes('saved.test'))!
      .trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Verify this device');
    expect(wrapper.find('input[type="password"]').exists()).toBe(false);
    await wrapper.get('form input').setValue('123456');
    await submit(wrapper);
    expect(bridge.service.CompleteReauthentication).toHaveBeenCalledWith('123456');
    expect(bridge.service.SignIn).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('report.txt');
    wrapper.unmount();
  });
  beforeEach(() => {
    vi.clearAllMocks();
    bridge.service.SessionStatus.mockResolvedValue({ next: 'files' });
    bridge.service.Profiles.mockResolvedValue([]);
    bridge.service.SetRemember.mockResolvedValue();
    localStorage.clear();
    delete document.documentElement.dataset.theme;
    bridge.service.Probe.mockResolvedValue({
      origin: 'https://server.test:58443',
      fingerprint,
      subject: 'CN=server',
      publicly_trusted: true,
    });
    bridge.service.Approve.mockResolvedValue();
    bridge.service.Batches.mockResolvedValue([]);
    bridge.service.Files.mockResolvedValue([{ id: 'f1', name: 'report.txt' }]);
    bridge.service.ChooseAndUpload.mockResolvedValue(1);
    bridge.service.ChooseAndDownload.mockResolvedValue(true);
    bridge.service.SignOut.mockResolvedValue();
  });

  it('onboards a new account through every mandatory step before showing files', async () => {
    const privateProbe = {
      origin: 'https://server.test:58443',
      fingerprint,
      subject: 'CN=server',
      publicly_trusted: false,
    };
    bridge.service.Probe.mockResolvedValue(privateProbe);
    bridge.service.ChooseServerCA.mockResolvedValue({
      ...privateProbe,
      authority_fingerprint: 'ca-fingerprint',
    });
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

    await wrapper.find('form input').setValue('server.test:58443');
    await submit(wrapper);
    expect(wrapper.find('[data-test="fingerprint"]').text()).toBe(
      fingerprint.match(/.{4}/g)?.join(' '),
    );
    expect(wrapper.text()).toContain('private certificate');
    const approval = () =>
      wrapper.findAll('button').find((button) => button.text().includes('They match'))!;
    expect(approval().attributes('disabled')).toBeDefined();
    await wrapper
      .findAll('button')
      .find((button) => button.text().includes('Choose server CA'))!
      .trigger('click');
    await flushPromises();
    expect(bridge.service.ChooseServerCA).toHaveBeenCalledWith('https://server.test:58443');
    expect(wrapper.text()).toContain('ca-fingerprint');
    expect(bridge.service.Approve).not.toHaveBeenCalled();

    await approval().trigger('click');
    await flushPromises();
    expect(bridge.service.Approve).toHaveBeenCalledWith(fingerprint);

    const [user, pass] = wrapper.findAll('form input');
    await user?.setValue('admin');
    await pass?.setValue('temporary');
    await submit(wrapper);
    expect(wrapper.text()).toContain('Choose your password');

    await wrapper.find('form input').setValue('a long permanent password');
    await submit(wrapper);
    expect(wrapper.find('img').attributes('src')).toBe('data:image/png;base64,AA==');
    expect(wrapper.text()).toContain('SECRETKEY');

    await wrapper.find('form input').setValue('123456');
    await submit(wrapper);
    expect(wrapper.text()).toContain('AAAA-1111');
    expect(wrapper.text()).not.toContain('SECRETKEY');
    expect(bridge.service.Files).not.toHaveBeenCalled();

    await wrapper.find('button').trigger('click');
    await wrapper.find('form input').setValue('654321');
    await submit(wrapper);
    expect(bridge.service.CompleteSignIn).toHaveBeenCalledWith('654321');
    expect(wrapper.text()).toContain('report.txt');
    expect(wrapper.text()).not.toContain('AAAA-1111');
  });

  it('keeps the user on the same step with the reason when an action fails', async () => {
    bridge.service.Probe.mockRejectedValue(new Error('could not reach a ServerSend server'));
    const wrapper = mount(App);
    await wrapper.find('form input').setValue('nowhere.test');
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
    await wrapper.find('form input').setValue('server.test:58443');
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await wrapper.find('form input').setValue('123456');
    await submit(wrapper);
    expect(wrapper.text()).toContain('Uploads and downloads from this session appear here.');

    const emit = (item: object) => bridge.listener?.({ data: [item] });
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

  it('browses folder names and breadcrumbs, shares one upload menu, and confirms deletion', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    bridge.service.Files.mockResolvedValue([{ id: 'folder', name: 'Photos', directory: true }]);
    bridge.service.DeleteEntry.mockResolvedValue();
    bridge.service.ChooseAndUploadFolder.mockResolvedValue();
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    bridge.service.Files.mockResolvedValue([{ id: 'file', name: 'image.jpg' }]);
    await wrapper.get('.folder-link').trigger('click');
    await flushPromises();
    expect(bridge.service.Files).toHaveBeenLastCalledWith('folder');
    expect(wrapper.get('.breadcrumbs').text()).toContain('Photos');
    await wrapper.get('.upload-menu > button').trigger('click');
    expect(wrapper.get('.upload-options').text()).toContain('Files…');
    expect(wrapper.get('.upload-options').text()).toContain('Folder…');
    await wrapper.get('.upload-options').findAll('button')[1]!.trigger('click');
    await flushPromises();
    expect(bridge.service.ChooseAndUploadFolder).toHaveBeenCalledWith('folder');
    await wrapper.get('[aria-label="Delete image.jpg"]').trigger('click');
    expect(bridge.service.DeleteEntry).not.toHaveBeenCalled();
    await wrapper.get('.delete-confirm .quiet').trigger('click');
    expect(wrapper.find('.delete-confirm').exists()).toBe(false);
    await wrapper.get('[aria-label="Delete image.jpg"]').trigger('click');
    bridge.service.DeleteEntry.mockRejectedValueOnce(new Error('Wait for uploads'));
    await wrapper.get('.delete-confirm .danger').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('Wait for uploads');
    expect(wrapper.find('.delete-confirm').exists()).toBe(true);
    bridge.service.Files.mockResolvedValue([]);
    await wrapper.get('.delete-confirm .danger').trigger('click');
    await flushPromises();
    expect(bridge.service.DeleteEntry).toHaveBeenLastCalledWith('file');
    expect(wrapper.find('.delete-confirm').exists()).toBe(false);
    await wrapper.get('.breadcrumbs button').trigger('click');
    await flushPromises();
    expect(bridge.service.Files).toHaveBeenLastCalledWith('');
    expect(wrapper.get('.breadcrumbs').text()).not.toContain('Photos');
    wrapper.unmount();
  });

  it('reports mixed and all-skipped selections with bounded, reviewable reasons', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    bridge.service.Batches.mockResolvedValue([
      {
        id: 1,
        name: 'Documents',
        state: 'done_with_skips',
        files: 2,
        completed: 2,
        failed: 0,
        directories: 3,
        skipped: 103,
        skippedItems: Array.from({ length: 100 }, (_, index) => ({
          path: `/Documents/link-${index}`,
          reason: 'Symbolic link; select the real target to upload it.',
        })),
        error: '',
        bytes: 3 * 1024 * 1024,
        startedAt: 1_000,
        finishedAt: 76_000,
      },
      {
        id: 2,
        name: 'shortcut',
        state: 'all_skipped',
        files: 0,
        completed: 0,
        failed: 0,
        directories: 0,
        skipped: 1,
        skippedItems: [
          { path: '/shortcut', reason: 'Symbolic link; select the real target to upload it.' },
        ],
        error: '',
      },
    ]);
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    const selections = wrapper.get('[aria-label="Upload selections"]');
    expect(selections.text()).toContain('Completed with skipped items');
    expect(selections.text()).toContain(
      '2 of 2 files uploaded · 3 folders created · 103 skipped · 0 failed',
    );
    expect(selections.text()).toContain('Zero files uploaded. Every selected item was skipped.');
    expect(selections.text()).toContain('Showing 100 of 103 skipped paths');
    expect(selections.findAll('details')).toHaveLength(2);
    expect(selections.findAll('[aria-label="Skipped paths and reasons"] li')).toHaveLength(101);
    expect(selections.text()).toContain('/Documents/link-99');
    expect(selections.text()).toContain("Select a link's real target");
    expect(selections.text()).toContain('Took 1m 15s · 3.0 MiB · 41.0 KiB/s average');
    expect(wrapper.find('progress').exists()).toBe(false);
    wrapper.unmount();
  });

  it('refreshes selection outcomes after a failed scan and keeps failures distinct from skips', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    bridge.service.Batches.mockResolvedValue([
      {
        id: 1,
        name: 'Unreadable folder',
        state: 'failed',
        files: 1,
        completed: 1,
        failed: 0,
        directories: 1,
        skipped: 1,
        skippedItems: [{ path: '/folder/link', reason: 'Symbolic link' }],
        error: 'Read directory: permission denied',
      },
    ]);
    bridge.service.ChooseAndUploadFolder.mockRejectedValueOnce(
      new Error('Read directory: permission denied'),
    );
    await wrapper.get('.upload-menu > button').trigger('click');
    await wrapper.get('.upload-options').findAll('button')[1]!.trigger('click');
    await flushPromises();
    const selections = wrapper.get('[aria-label="Upload selections"]');
    expect(selections.text()).toContain('Failed');
    expect(selections.text()).toContain('Read directory: permission denied');
    expect(selections.text()).not.toContain('Completed with skipped items');
    expect(wrapper.get('[role="alert"]').text()).toContain('Read directory: permission denied');
    expect(wrapper.get('.upload-menu > button').attributes('disabled')).toBeUndefined();
    wrapper.unmount();
  });

  it('offers retry when upload summaries cannot be read', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    bridge.service.Batches.mockRejectedValueOnce(new Error('Summary unavailable'));
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    expect(wrapper.get('[aria-label="Upload selections"]').text()).toContain(
      'Upload summaries unavailable: Summary unavailable',
    );
    await wrapper.get('[aria-label="Upload selections"] button').trigger('click');
    await flushPromises();
    expect(wrapper.get('[aria-label="Upload selections"]').text()).not.toContain(
      'Upload summaries unavailable',
    );
    wrapper.unmount();
  });

  it('bounds finished transfer history while retaining current work', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    const item = (id: number, state: string) => ({
      id,
      name: `${state}-${id}`,
      direction: 'upload',
      state,
      done: 1,
      total: 1,
      speed: 0,
      error: '',
    });
    bridge.service.Files.mockClear();
    // One batch per state, as a large folder scan and its uploads produce.
    bridge.listener?.({ data: [item(1, 'active')] });
    bridge.listener?.({
      data: Array.from({ length: 501 }, (_, index) => item(index + 2, 'done')),
    });
    bridge.listener?.({
      data: Array.from({ length: 5000 }, (_, index) => item(index + 600, 'queued')),
    });
    bridge.listener?.({ data: [item(503, 'done')] });
    await flushPromises();
    const queue = wrapper.get('[aria-label="Transfer queue"]');
    expect(queue.findAll(':scope > li')).toHaveLength(200);
    expect(queue.text()).toContain('active-1');
    expect(queue.text()).toContain('queued-600');
    expect(queue.text()).not.toContain('queued-620');
    expect(queue.text()).toContain('done-503');
    expect(queue.text()).not.toContain('done-2');
    expect(wrapper.text()).toContain('Showing 200 of 5201 transfers');
    // Completed uploads refresh the listing at most once per interval.
    expect(bridge.service.Files).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it('cancels running selections and individual transfers', async () => {
    bridge.service.SignIn.mockResolvedValue({ next: 'second_factor' });
    bridge.service.CompleteSignIn.mockResolvedValue({
      username: 'alice',
      origin: 'https://server.test',
      serverId: 's',
    });
    bridge.service.CancelTransfer.mockResolvedValue();
    bridge.service.CancelBatch.mockResolvedValue();
    bridge.service.Batches.mockResolvedValue([
      {
        id: 7,
        name: 'Photos',
        state: 'running',
        files: 3,
        completed: 1,
        failed: 0,
        cancelled: 1,
        directories: 1,
        skipped: 0,
        skippedItems: [],
        error: '',
        bytes: 1,
        startedAt: 1,
        finishedAt: 0,
      },
    ]);
    const wrapper = mount(App);
    await submit(wrapper);
    await wrapper.find('button').trigger('click');
    await flushPromises();
    await submit(wrapper);
    await submit(wrapper);
    bridge.listener?.({
      data: [
        {
          id: 3,
          name: 'a.jpg',
          direction: 'upload',
          state: 'queued',
          done: 0,
          total: 1,
          error: '',
        },
        { id: 4, name: 'b.jpg', direction: 'upload', state: 'done', done: 1, total: 1, error: '' },
      ],
    });
    await flushPromises();
    const selections = wrapper.get('[aria-label="Upload selections"]');
    expect(selections.text()).toContain('1 cancelled');
    await selections.get('[aria-label="Cancel Photos"]').trigger('click');
    expect(bridge.service.CancelBatch).toHaveBeenCalledWith(7);
    const queue = wrapper.get('[aria-label="Transfer queue"]');
    expect(queue.find('[aria-label="Cancel b.jpg"]').exists()).toBe(false);
    await queue.get('[aria-label="Cancel a.jpg"]').trigger('click');
    expect(bridge.service.CancelTransfer).toHaveBeenCalledWith(3);
    wrapper.unmount();
  });

  it('applies and persists the chosen theme', async () => {
    const wrapper = mount(App);
    expect(document.documentElement.dataset.theme).toBeUndefined();
    await wrapper.find('input[value="dark"]').setValue(true);
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(localStorage.getItem('serversend-theme')).toBe('dark');
    wrapper.unmount();
    mount(App);
    expect(document.documentElement.dataset.theme).toBe('dark');
  });
});
