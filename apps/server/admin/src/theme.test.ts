import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import ThemePicker from './components/ThemePicker.vue';
import bootstrap from '../public/theme-init.js?raw';

describe('admin appearance', () => {
  let wrapper: VueWrapper | undefined;
  let media: EventTarget & { matches: boolean };

  beforeEach(() => {
    localStorage.clear();
    delete document.documentElement.dataset.theme;
    media = Object.assign(new EventTarget(), { matches: false });
    vi.stubGlobal('matchMedia', () => media);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
    vi.unstubAllGlobals();
  });

  it.each(['light', 'dark', 'system'])(
    'saves the %s choice and restores it on remount',
    async (theme) => {
      wrapper = mount(ThemePicker);
      // Select another radio first so the initially selected System choice
      // also exercises an actual keyboard/radio change event.
      if (theme === 'system') await wrapper.get('input[value="dark"]').setValue();
      await wrapper.get(`input[value="${theme}"]`).setValue();
      expect(localStorage.getItem('serversend.admin.theme')).toBe(theme);
      expect(document.documentElement.dataset.theme).toBe(theme === 'system' ? 'light' : theme);
      wrapper.unmount();
      wrapper = mount(ThemePicker);
      expect(wrapper.get<HTMLInputElement>(`input[value="${theme}"]`).element.checked).toBe(true);
      expect(wrapper.get('legend').text()).toBe('Appearance');
      expect(wrapper.findAll('input[type="radio"]')).toHaveLength(3);
    },
  );

  it('follows OS changes only when system is selected and unregisters listeners', async () => {
    const remove = vi.spyOn(media, 'removeEventListener');
    wrapper = mount(ThemePicker);
    media.matches = true;
    media.dispatchEvent(new Event('change'));
    expect(document.documentElement.dataset.theme).toBe('dark');
    await wrapper.get('input[value="light"]').setValue();
    media.dispatchEvent(new Event('change'));
    expect(document.documentElement.dataset.theme).toBe('light');
    await wrapper.get('input[value="system"]').setValue();
    expect(document.documentElement.dataset.theme).toBe('dark');
    wrapper.unmount();
    wrapper = undefined;
    expect(remove).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('syncs other tabs and falls back to system when storage is cleared', () => {
    wrapper = mount(ThemePicker);
    localStorage.setItem('serversend.admin.theme', 'dark');
    window.dispatchEvent(new StorageEvent('storage', { key: 'serversend.admin.theme' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
    localStorage.clear();
    window.dispatchEvent(new StorageEvent('storage', { key: null }));
    expect(document.documentElement.dataset.theme).toBe('light');
  });

  it('works when browser storage is unavailable and explains failed persistence', async () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    });
    wrapper = mount(ThemePicker);
    await wrapper.get('input[value="dark"]').setValue();
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(wrapper.get('[role="status"]').text()).toContain('could not save');
  });

  it.each([
    ['dark', false, 'dark'],
    ['light', true, 'light'],
    ['system', true, 'dark'],
    ['system', false, 'light'],
    ['invalid', true, 'dark'],
    [null, false, 'light'],
  ])('applies stored %s with OS dark=%s before the Vue app loads', (stored, dark, expected) => {
    if (stored) localStorage.setItem('serversend.admin.theme', stored);
    media.matches = Boolean(dark);
    // Execute the actual blocking production script without mounting Vue.
    window.eval(bootstrap);
    expect(document.documentElement.dataset.theme).toBe(expected);
    wrapper = mount(ThemePicker);
    expect(document.documentElement.dataset.theme).toBe(expected);
  });

  it('applies the OS theme before render when storage is blocked', () => {
    media.matches = true;
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked');
      },
    });
    window.eval(bootstrap);
    expect(document.documentElement.dataset.theme).toBe('dark');
  });
});
