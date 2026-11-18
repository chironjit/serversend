import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import App from './App.vue';

type Listener = (event: { data: unknown }) => void;
const bridge = vi.hoisted(() => ({
  listener: undefined as Listener | undefined,
  service: {
    VaultUnlocked: vi.fn<() => Promise<boolean>>().mockResolvedValue(false),
    PrivateMode: vi.fn<() => Promise<boolean>>().mockResolvedValue(false),
    FilesPage: vi.fn<
      (
        parent: string,
        cursor: string,
      ) => Promise<{
        files: { id: string; name: string; directory?: boolean }[];
        nextCursor: string;
        revision: string;
        snapshot: string;
      }>
    >(),
    CatalogChanged: vi.fn<(revision: string) => Promise<boolean>>(),
    Transfers: vi.fn<() => Promise<unknown[]>>(),
    QueueAction: vi.fn<(id: number, batch: boolean, action: string) => Promise<void>>(),
    QueueHealth: vi.fn<() => Promise<string>>().mockResolvedValue(''),
    SetBandwidth: vi.fn<(bytes: number) => Promise<void>>(),
    Bandwidth: vi.fn<() => Promise<number>>(),
    ChooseAndRestore: vi.fn<(ids: string[], policy: string) => Promise<boolean>>(),
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
      scope: 's1/a1',
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
      scope: 's1/a1',
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
    bridge.service.FilesPage.mockImplementation(async (parent) => ({
      files: (await bridge.service.Files(parent)) ?? [],
      nextCursor: '',
      revision: '1',
      snapshot: 'snapshot',
    }));
    bridge.service.CatalogChanged.mockResolvedValue(false);
    bridge.service.Transfers.mockResolvedValue([]);
    bridge.service.Bandwidth.mockResolvedValue(0);
    bridge.service.SetBandwidth.mockResolvedValue();
    bridge.service.ChooseAndRestore.mockResolvedValue(true);
    bridge.service.QueueAction.mockImplementation(async (id, batch, action) => {
      if (action === 'cancel')
        await (batch ? bridge.service.CancelBatch(id) : bridge.service.CancelTransfer(id));
    });
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
    expect(wrapper.text()).toContain('Saved uploads and downloads appear here.');

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
    const selections = wrapper.get('[aria-label="Transfer selections"]');
    expect(selections.text()).toContain('Completed with skipped items');
    expect(selections.text()).toContain(
      '2 of 2 files transferred · 3 folders created · 103 skipped · 0 failed',
    );
    expect(selections.text()).toContain('Zero files transferred. Every selected item was skipped.');
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
    const selections = wrapper.get('[aria-label="Transfer selections"]');
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
    expect(wrapper.get('[aria-label="Transfer selections"]').text()).toContain(
      'Transfer summaries unavailable: Summary unavailable',
    );
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Retry summaries')!
      .trigger('click');
    await flushPromises();
    expect(wrapper.get('[aria-label="Transfer selections"]').text()).not.toContain(
      'Transfer summaries unavailable',
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
    expect(wrapper.text()).toContain('Showing up to 200 recent transfers');
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
    const selections = wrapper.get('[aria-label="Transfer selections"]');
    expect(selections.text()).toContain('1 cancelled');
    await selections.get('[aria-label="Cancel Photos"]').trigger('click');
    expect(bridge.service.QueueAction).toHaveBeenCalledWith(7, true, 'cancel');
    await flushPromises();
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
      ],
    });
    await flushPromises();
    const queue = wrapper.get('[aria-label="Transfer queue"]');
    expect(queue.find('[aria-label="Cancel b.jpg"]').exists()).toBe(false);
    await queue.get('[aria-label="Cancel a.jpg"]').trigger('click');
    expect(bridge.service.QueueAction).toHaveBeenCalledWith(3, false, 'cancel');
    wrapper.unmount();
  });

  async function mountLibrary() {
    bridge.service.Profiles.mockResolvedValue([
      { origin: 'https://saved.test:58443', username: 'alice', remember: true },
    ]);
    bridge.service.OpenProfile.mockResolvedValue({
      username: 'alice',
      origin: 'https://saved.test:58443',
      serverId: 's1',
      scope: 's1/a1',
    });
    const wrapper = mount(App);
    await flushPromises();
    await wrapper
      .findAll('button')
      .find((button) => button.text().includes('saved.test'))!
      .trigger('click');
    await flushPromises();
    return wrapper;
  }

  it('keeps catalog rows and selections bounded to one page and navigates both directions', async () => {
    bridge.service.FilesPage.mockImplementation(async (_parent, cursor) => ({
      files: Array.from({ length: 100 }, (_, index) => ({
        id: String(index + (cursor ? 100 : 0)),
        name: `file-${index + (cursor ? 100 : 0)}`,
      })),
      nextCursor: cursor ? '' : 'next-page',
      revision: '7',
      snapshot: 'snapshot-7',
    }));
    const wrapper = await mountLibrary();
    expect(wrapper.get('[aria-label="Files"]').findAll(':scope > li')).toHaveLength(100);
    await wrapper.get('[aria-label="Select file-0"]').setValue(true);
    expect(wrapper.text()).toContain('Restore selected (1)');
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Next files')!
      .trigger('click');
    await flushPromises();
    expect(bridge.service.FilesPage).toHaveBeenLastCalledWith('', 'next-page');
    expect(wrapper.get('[aria-label="Files"]').text()).toContain('file-100');
    expect(wrapper.get('[aria-label="Files"]').text()).not.toContain('file-0');
    expect(wrapper.text()).toContain('Restore selected (0)');
    expect(wrapper.text()).toContain('Page 2');
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Previous files')!
      .trigger('click');
    await flushPromises();
    expect(bridge.service.FilesPage).toHaveBeenLastCalledWith('', '');
    expect(wrapper.text()).toContain('Page 1');
    wrapper.unmount();
  });

  it('discards an expired continuation and clears selections before showing the refreshed first page', async () => {
    let refreshed = false;
    bridge.service.FilesPage.mockImplementation(async (_parent, cursor) => {
      if (cursor) {
        refreshed = true;
        throw new Error('snapshot_expired: catalog changed');
      }
      return {
        files: [{ id: refreshed ? 'new' : 'old', name: refreshed ? 'new.txt' : 'old.txt' }],
        nextCursor: refreshed ? '' : 'expired',
        revision: refreshed ? '2' : '1',
        snapshot: 's',
      };
    });
    const wrapper = await mountLibrary();
    await wrapper.get('[aria-label="Select old.txt"]').setValue(true);
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Next files')!
      .trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('The folder changed or its page expired');
    expect(wrapper.get('[aria-label="Files"]').text()).toContain('new.txt');
    expect(wrapper.get('[aria-label="Files"]').text()).not.toContain('old.txt');
    expect(wrapper.text()).toContain('Restore selected (0)');
    expect(
      wrapper
        .findAll('button')
        .find((button) => button.text() === 'Previous files')!
        .attributes('disabled'),
    ).toBeDefined();
    wrapper.unmount();
  });

  it('reconciles catalog revisions after reconnect without replacing an unchanged current page', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    let wrapper: Awaited<ReturnType<typeof mountLibrary>> | undefined;
    try {
      wrapper = await mountLibrary();
      bridge.service.FilesPage.mockClear();
      await vi.advanceTimersByTimeAsync(30000);
      expect(bridge.service.CatalogChanged).toHaveBeenLastCalledWith('1');
      expect(bridge.service.FilesPage).not.toHaveBeenCalled();
      bridge.service.CatalogChanged.mockResolvedValue(true);
      bridge.service.FilesPage.mockResolvedValue({
        files: [{ id: 'new', name: 'after-reconnect.txt' }],
        nextCursor: '',
        revision: '2',
        snapshot: 'new-snapshot',
      });
      await vi.advanceTimersByTimeAsync(30000);
      expect(bridge.service.FilesPage).toHaveBeenLastCalledWith('', '');
      expect(wrapper.get('[aria-label="Files"]').text()).toContain('after-reconnect.txt');
      expect(wrapper.text()).toContain('The folder changed. Showing the latest first page.');
    } finally {
      wrapper?.unmount();
      vi.useRealTimers();
    }
  });

  it('restores selected files and folders with an explicit existing-file policy', async () => {
    bridge.service.Files.mockResolvedValue([
      { id: 'folder', name: 'Photos', directory: true },
      { id: 'file', name: 'report.txt' },
    ]);
    const wrapper = await mountLibrary();
    await wrapper.get('[aria-label="Select Photos"]').setValue(true);
    await wrapper.get('[aria-label="Select report.txt"]').setValue(true);
    await wrapper
      .findAll('button')
      .find((button) => button.text().startsWith('Restore selected'))!
      .trigger('click');
    await flushPromises();
    expect(bridge.service.ChooseAndRestore).toHaveBeenLastCalledWith(['folder', 'file'], 'fail');
    await wrapper.get('[aria-label="Existing file policy"]').setValue('replace');
    await wrapper
      .findAll('button')
      .find((button) => button.text().startsWith('Restore selected'))!
      .trigger('click');
    await flushPromises();
    expect(bridge.service.ChooseAndRestore).toHaveBeenLastCalledWith(['folder', 'file'], 'replace');
    await wrapper.get('[aria-label="Existing file policy"]').setValue('skip');
    await wrapper.get('[aria-label="Restore folder Photos"]').trigger('click');
    await flushPromises();
    expect(bridge.service.ChooseAndRestore).toHaveBeenLastCalledWith(['folder'], 'skip');
    bridge.service.ChooseAndRestore.mockRejectedValueOnce(new Error('Destination is not writable'));
    await wrapper.get('[aria-label="Restore folder Photos"]').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('Destination is not writable');
    expect(
      wrapper.get('[aria-label="Restore folder Photos"]').attributes('disabled'),
    ).toBeUndefined();
    wrapper.unmount();
  });

  it('loads durable wait states and offers pause, resume, retry and persisted bandwidth controls', async () => {
    bridge.service.Bandwidth.mockResolvedValue(1572864);
    bridge.service.Batches.mockResolvedValue([
      {
        id: 7,
        name: 'Documents',
        state: 'running',
        files: 3,
        completed: 0,
        directories: 0,
        skipped: 0,
        failed: 0,
        error: '',
      },
    ]);
    bridge.service.Transfers.mockResolvedValue([
      {
        id: 1,
        name: 'paused.txt',
        direction: 'upload',
        state: 'paused',
        done: 1,
        total: 2,
        error: '',
      },
      {
        id: 2,
        name: 'offline.txt',
        direction: 'download',
        state: 'waiting_network',
        done: 1,
        total: 2,
        error: 'Connection unavailable',
      },
      {
        id: 3,
        name: 'failed.txt',
        direction: 'upload',
        state: 'failed',
        done: 0,
        total: 2,
        error: 'Source missing',
      },
    ]);
    const wrapper = await mountLibrary();
    expect(wrapper.text()).toContain('Waiting for connection');
    expect(wrapper.text()).toContain('Connection unavailable');
    expect((wrapper.get('#bandwidth').element as HTMLInputElement).value).toBe('1.5');
    await wrapper.get('[aria-label="Pause Documents"]').trigger('click');
    await flushPromises();
    expect(bridge.service.QueueAction).toHaveBeenLastCalledWith(7, true, 'pause');
    await wrapper.get('[aria-label="Resume paused.txt"]').trigger('click');
    await flushPromises();
    expect(bridge.service.QueueAction).toHaveBeenLastCalledWith(1, false, 'resume');
    await wrapper.get('[aria-label="Retry failed.txt"]').trigger('click');
    await flushPromises();
    expect(bridge.service.QueueAction).toHaveBeenLastCalledWith(3, false, 'retry');
    await wrapper.get('#bandwidth').setValue('2.25');
    await wrapper
      .findAll('form')
      .find((form) => form.find('#bandwidth').exists())!
      .trigger('submit');
    await flushPromises();
    expect(bridge.service.SetBandwidth).toHaveBeenLastCalledWith(2359296);
    bridge.service.QueueAction.mockRejectedValueOnce(new Error('Queue update failed'));
    await wrapper.get('[aria-label="Resume paused.txt"]').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('Queue update failed');
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
  it('discards buffered transfer events from another account', async () => {
    const wrapper = await mountLibrary();
    bridge.listener?.({
      data: [
        {
          id: 900,
          scope: 'old-server/old-account',
          name: 'old-private-name',
          direction: 'upload',
          state: 'active',
          done: 1,
          total: 2,
          error: '',
        },
        {
          id: 901,
          scope: 's1/a1',
          name: 'current-file',
          direction: 'upload',
          state: 'active',
          done: 1,
          total: 2,
          error: '',
        },
      ],
    });
    await flushPromises();
    expect(wrapper.text()).not.toContain('old-private-name');
    expect(wrapper.text()).toContain('current-file');
    wrapper.unmount();
  });
});
