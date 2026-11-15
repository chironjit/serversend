import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import HelloWorld from './HelloWorld.vue';

const bridge = vi.hoisted(() => ({
  greet: vi.fn<(name: string) => Promise<string>>(),
  on: vi.fn<(name: string, callback: (event: { data: string }) => void) => () => void>(),
  stop: vi.fn<() => void>(),
  reload: vi.fn<() => void>(),
}));
vi.mock('@wailsio/runtime', () => ({
  Events: { On: bridge.on },
  WML: { Reload: bridge.reload },
}));
vi.mock('../../bindings/github.com/chironjit/serversend/apps/desktop', () => ({
  GreetService: { Greet: bridge.greet },
}));

describe('desktop scaffold bridge', () => {
  let wrapper: VueWrapper | undefined;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    bridge.on.mockReturnValue(bridge.stop);
    bridge.greet.mockImplementation((name: string) => Promise.resolve(`Hello ${name}!`));
    vi.stubGlobal(
      'matchMedia',
      vi.fn<() => { matches: boolean }>().mockReturnValue({ matches: false }),
    );
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it.each(['Ada', '', 'Zoë 東京'])(
    'calls Go with the entered name %j and displays its response',
    async (name) => {
      wrapper = mount(HelloWorld);
      await wrapper.get('input').setValue(name);
      await wrapper.get('button').trigger('click');
      await flushPromises();
      expect(bridge.greet).toHaveBeenCalledWith(name || 'anonymous');
      expect(wrapper.get('[aria-label="result"]').text()).toBe(`Hello ${name || 'anonymous'}!`);
      expect(wrapper.get('[role="status"]').classes()).toContain('is-visible');
      vi.advanceTimersByTime(4000);
      expect(wrapper.get('[role="status"]').classes()).not.toContain('is-visible');
    },
  );

  it('renders service output as text rather than executable HTML', async () => {
    bridge.greet.mockResolvedValue('<img src=x onerror=alert(1)>');
    wrapper = mount(HelloWorld);
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(wrapper.get('[aria-label="result"]').text()).toBe('<img src=x onerror=alert(1)>');
    expect(wrapper.find('[aria-label="result"] img').exists()).toBe(false);
  });

  it('shows a retry action without exposing the service error, then recovers', async () => {
    bridge.greet.mockRejectedValueOnce(new Error('internal diagnostic'));
    wrapper = mount(HelloWorld);
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('Please try again');
    expect(wrapper.text()).not.toContain('internal diagnostic');
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.get('[aria-label="result"]').text()).toBe('Hello anonymous!');
  });

  it.each([false, true])('shows time events with compact display=%j', async (compact) => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn<() => { matches: boolean }>().mockReturnValue({ matches: compact }),
    );
    wrapper = mount(HelloWorld);
    expect(bridge.on).toHaveBeenCalledWith('time', expect.any(Function));
    const callback = bridge.on.mock.calls[0][1] as (event: { data: string }) => void;
    callback({ data: 'Sat, 14 Nov 2026 12:34:56 AEDT' });
    await wrapper.vm.$nextTick();
    expect(wrapper.get('.footer-time').text()).toBe(
      compact ? '12:34:56' : 'Sat, 14 Nov 2026 12:34:56 AEDT',
    );
    expect(bridge.reload).toHaveBeenCalledOnce();
  });

  it('unregisters events and clears the toast timer on unmount', async () => {
    wrapper = mount(HelloWorld);
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    wrapper.unmount();
    wrapper = undefined;
    expect(bridge.stop).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });
});
