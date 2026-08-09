import { useEffect, useState } from 'react'
import { getSchedulerConfig, updateSchedulerConfig } from '../api/client'
import type { UpdateSchedulerConfigRequest, TimeBlockDTO } from '../api/types'
import LoadingState from '../components/LoadingState'
import ErrorState from '../components/ErrorState'
import TimeBlockEditor from '../components/TimeBlockEditor'

interface FormState {
  enabled: boolean
  aggressiveness: number
  reserved_blocks: TimeBlockDTO[]
  preferred_windows: TimeBlockDTO[]
  max_budget_usd?: number
}

export default function SchedulerConfig() {
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<unknown | null>(null)
  const [lastSaved, setLastSaved] = useState<Date | null>(null)

  const [form, setForm] = useState<FormState>({
    enabled: false,
    aggressiveness: 50,
    reserved_blocks: [],
    preferred_windows: [],
    max_budget_usd: undefined,
  })

  // Load config on mount
  useEffect(() => {
    let cancelled = false

    const loadConfig = async () => {
      try {
        const response = await getSchedulerConfig()
        if (!cancelled) {
          setForm({
            enabled: response.enabled,
            aggressiveness: response.aggressiveness,
            reserved_blocks: response.reserved_blocks,
            preferred_windows: response.preferred_windows,
            max_budget_usd: response.max_budget_usd,
          })
          // updated_at is a Go zero-value timestamp (year 1) for a config
          // that's never actually been saved yet — only show "last saved"
          // once there's a real one.
          setLastSaved(response.updated_at > 0 ? new Date(response.updated_at * 1000) : null)
          setLoadError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setLoadError(err)
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }

    loadConfig()
    return () => {
      cancelled = true
    }
  }, [])

  const handleSave = async () => {
    setSaving(true)
    setSaveError(null)

    try {
      const request: UpdateSchedulerConfigRequest = {
        enabled: form.enabled,
        aggressiveness: form.aggressiveness,
        reserved_blocks: form.reserved_blocks,
        preferred_windows: form.preferred_windows,
        ...(form.max_budget_usd !== undefined && { max_budget_usd: form.max_budget_usd }),
      }

      const response = await updateSchedulerConfig(request)

      // Replace form state with server response (authoritative)
      setForm({
        enabled: response.enabled,
        aggressiveness: response.aggressiveness,
        reserved_blocks: response.reserved_blocks,
        preferred_windows: response.preferred_windows,
        max_budget_usd: response.max_budget_usd,
      })
      setLastSaved(new Date(response.updated_at * 1000))
    } catch (err) {
      setSaveError(err)
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <LoadingState label="Loading scheduler configuration…" />
  }

  if (loadError !== null) {
    return (
      <div className="stack">
        <h1>Scheduler configuration</h1>
        <ErrorState error={loadError} />
      </div>
    )
  }

  return (
    <div className="stack">
      <h1>Scheduler configuration</h1>

      <div className="card stack">
        {/* Enabled toggle */}
        <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
          />
          <span>Enabled</span>
        </label>

        {/* Aggressiveness slider */}
        <div className="stack">
          <label>
            Aggressiveness: <strong>{form.aggressiveness}%</strong>
          </label>
          <input
            type="range"
            min="0"
            max="100"
            value={form.aggressiveness}
            onChange={(e) => setForm({ ...form, aggressiveness: Number(e.target.value) })}
          />
        </div>

        {/* Max budget */}
        <div className="stack">
          <label htmlFor="max-budget">Max budget (USD, optional)</label>
          <input
            id="max-budget"
            type="number"
            step="0.01"
            min="0"
            value={form.max_budget_usd ?? ''}
            onChange={(e) => {
              const val = e.target.value
              setForm({
                ...form,
                max_budget_usd: val === '' ? undefined : Number(val),
              })
            }}
            placeholder="No limit"
          />
        </div>

        {/* Reserved blocks */}
        <TimeBlockEditor
          label="Reserved blocks"
          blocks={form.reserved_blocks}
          onChange={(blocks) => setForm({ ...form, reserved_blocks: blocks })}
        />

        {/* Preferred windows */}
        <TimeBlockEditor
          label="Preferred windows"
          blocks={form.preferred_windows}
          onChange={(blocks) => setForm({ ...form, preferred_windows: blocks })}
        />
      </div>

      {/* Save button and status */}
      <div className="row">
        <button
          className="primary"
          onClick={handleSave}
          disabled={saving}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>

        {lastSaved && (
          <span className="muted">
            Last saved: {lastSaved.toLocaleString()}
          </span>
        )}
      </div>

      {/* Save error */}
      {saveError !== null && <ErrorState error={saveError} />}
    </div>
  )
}
