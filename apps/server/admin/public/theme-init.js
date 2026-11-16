// This blocking, same-origin script applies the theme before the app or CSS
// renders. Keep its storage rules aligned with src/theme.ts.
(() => {
  let preference = 'system';
  try {
    const stored = localStorage.getItem('serversend.admin.theme');
    if (stored === 'light' || stored === 'dark') preference = stored;
  } catch {
    // A restricted browser can still follow the system theme.
  }
  document.documentElement.dataset.theme =
    preference === 'system'
      ? window.matchMedia('(prefers-color-scheme: dark)').matches
        ? 'dark'
        : 'light'
      : preference;
})();
