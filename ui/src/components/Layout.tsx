import { useEffect, useState, type ReactNode } from 'react'
import { getKillSwitch } from '../api/client'
import type { ThemePreference } from '../theme'
import { getStoredTheme, setStoredTheme } from '../theme'

const NAV = [
  { href: '#/', label: 'New job' },
  { href: '#/jobs', label: 'Jobs' },
  { href: '#/dashboard', label: 'Usage' },
  { href: '#/scheduler', label: 'Scheduler' },
]

const THEME_OPTIONS: ThemePreference[] = ['system', 'light', 'dark']

// KillSwitchPollMs is how often Layout's header badge re-checks kill-switch
// status. Layout is mounted once for the whole app (see App.tsx) and never
// remounts on navigation, so a one-shot fetch would go stale the moment the
// switch is toggled anywhere — the Dashboard's own controls, the CLI, or
// another browser tab. Polling is the simplest fix that covers all three
// without a shared context or prop-drilling into every page.
const KillSwitchPollMs = 5000

// Layout wraps every page: <Layout><SomePage/></Layout>. It owns the nav,
// theme toggle, and a small kill-switch status strip (polled — the full
// halt/resume controls live on the Dashboard page).
export default function Layout({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<ThemePreference>(getStoredTheme())
  const [halted, setHalted] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false
    const fetchStatus = () => {
      getKillSwitch()
        .then((r) => {
          if (!cancelled) setHalted(r.halted)
        })
        .catch(() => {
          if (!cancelled) setHalted(null)
        })
    }
    fetchStatus()
    const interval = setInterval(fetchStatus, KillSwitchPollMs)
    return () => {
      cancelled = true
      clearInterval(interval)
    }
  }, [])

  function handleThemeChange(pref: ThemePreference) {
    setTheme(pref)
    setStoredTheme(pref)
  }

  return (
    <div className="stack" style={{ minHeight: '100vh' }}>
      <header
        className="row"
        style={{
          borderBottom: '1px solid var(--border)',
          padding: '10px 20px',
          justifyContent: 'space-between',
        }}
      >
        <nav className="row" style={{ gap: 16 }}>
          <strong>tokenwarden</strong>
          {NAV.map((item) => (
            <a key={item.href} href={item.href}>
              {item.label}
            </a>
          ))}
        </nav>
        <div className="row">
          {halted !== null && (
            <span
              className="muted"
              style={{ color: halted ? 'var(--danger)' : 'var(--success)' }}
            >
              {halted ? 'Halted' : 'Running'}
            </span>
          )}
          <select
            aria-label="Theme"
            value={theme}
            onChange={(e) => handleThemeChange(e.target.value as ThemePreference)}
          >
            {THEME_OPTIONS.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>
      </header>
      <main style={{ padding: '20px', flex: 1 }}>{children}</main>
    </div>
  )
}
