export type ThemePreference = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'tokenwarden-theme'

export function getStoredTheme(): ThemePreference {
  const v = localStorage.getItem(STORAGE_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

export function setStoredTheme(pref: ThemePreference): void {
  localStorage.setItem(STORAGE_KEY, pref)
  applyTheme(pref)
}

export function applyTheme(pref: ThemePreference): void {
  const resolved =
    pref === 'system'
      ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : pref
  document.documentElement.dataset.theme = resolved
}

// Called once at startup. Keeps the resolved theme in sync with the OS
// while the stored preference is 'system'.
export function initTheme(): void {
  applyTheme(getStoredTheme())
  window
    .matchMedia('(prefers-color-scheme: dark)')
    .addEventListener('change', () => {
      if (getStoredTheme() === 'system') applyTheme('system')
    })
}
