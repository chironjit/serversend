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
    Batches: vi.fn<() => Promise<unknown[] | null>>(),
    Files:
      vi.fn<
        (parent: string) => Promise<{ id: string; name: string; directory?: boolean }[] | null>
      >(),
    DeleteEntry: vi.fn<(id: string) => Promise<void>>(),
    ChooseAndUploadFolder: vi.fn<(parent: string) => Promise<void>>(),
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
    bridge.service.Batches.mockResolvedValue([]);
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

    await wrapper.find('form input').setValue('server.test:58443');
    await submit(wrapper);
    expect(wrapper.find('[data-test="fingerprint"]').text()).toBe(
      fingerprint.match(/.{4}/g)?.join(' '),
    );
    expect(wrapper.text()).toContain('private certificate');
    expect(bridge.service.Approve).not.toHaveBeenCalled();

    await wrapper.find('button').trigger('click');
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
    bridge.listener?.({
      data: {
        id: 1,
        name: 'still running',
        direction: 'download',
        state: 'active',
        done: 1,
        total: 100,
        speed: 1,
        error: '',
      },
    });
    for (let id = 2; id < 503; id += 1) {
      bridge.listener?.({
        data: {
          id,
          name: `finished-${id}`,
          direction: 'download',
          state: 'done',
          done: 1,
          total: 1,
          speed: 0,
          error: '',
        },
      });
    }
    await flushPromises();
    const queue = wrapper.get('[aria-label="Transfer queue"]');
    expect(queue.findAll(':scope > li')).toHaveLength(501);
    expect(queue.text()).toContain('still running');
    expect(queue.text()).toContain('finished-502');
    expect(queue.findAll('span.min-w-0').map((name) => name.text())).not.toContain('finished-2');
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
