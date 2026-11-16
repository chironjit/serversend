import { onMounted, onUnmounted, ref } from 'vue';

export type ThemePreference = 'light' | 'dark' | 'system';
const storageKey = 'serversend.admin.theme';

function readPreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(storageKey);
    return stored === 'light' || stored === 'dark' ? stored : 'system';
  } catch {
    return 'system';
  }
}

export function useTheme() {
  const preference = ref<ThemePreference>(readPreference());
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const storageUnavailable = ref(false);

  function apply() {
    document.documentElement.dataset.theme =
      preference.value === 'system' ? (media.matches ? 'dark' : 'light') : preference.value;
  }

  function setPreference(value: ThemePreference) {
    preference.value = value;
    apply();
    try {
      localStorage.setItem(storageKey, value);
      storageUnavailable.value = false;
    } catch {
      storageUnavailable.value = true;
    }
  }

  function syncStorage(event: StorageEvent) {
    if (event.key === storageKey || event.key === null) {
      preference.value = readPreference();
      apply();
    }
  }

  onMounted(() => {
    apply();
    media.addEventListener('change', apply);
    window.addEventListener('storage', syncStorage);
  });
  onUnmounted(() => {
    media.removeEventListener('change', apply);
    window.removeEventListener('storage', syncStorage);
  });

  return { preference, setPreference, storageUnavailable };
}
