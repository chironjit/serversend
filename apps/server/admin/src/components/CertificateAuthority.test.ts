import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import CertificateAuthority from './CertificateAuthority.vue';
afterEach(() => vi.unstubAllGlobals());
it('shows the CA fingerprint and a download link', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ sha256: 'AB:CD' }))),
  );
  const w = mount(CertificateAuthority);
  await flushPromises();
  expect(w.get('code').text()).toBe('AB:CD');
  expect(w.get('a').attributes('href')).toBe('/api/v1/admin/ca.crt');
  w.unmount();
});
it('explains when external certificates are used', async () => {
  const body = { error: { code: 'not_found', message: 'externally supplied certificates' } };
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(body), { status: 404 })),
  );
  const w = mount(CertificateAuthority);
  await flushPromises();
  expect(w.text()).toContain('externally supplied certificates');
  expect(w.find('a').exists()).toBe(false);
  w.unmount();
});
