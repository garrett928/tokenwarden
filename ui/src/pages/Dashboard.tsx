import { useEffect, useState } from 'react'
import LoadingState from '../components/LoadingState'
import ErrorState from '../components/ErrorState'
import { getUsage, getKillSwitch, haltKillSwitch, resumeKillSwitch } from '../api/client'
import type { UsageResponse, KillSwitchResponse } from '../api/types'

function formatCurrency(usd: number): string {
  return `$${usd.toFixed(2)}`
}

function formatTimeAgo(seconds: number): string {
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  return `${Math.floor(seconds / 3600)}h ago`
}

function formatDateTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString()
}

export default function Dashboard() {
  const [usage, setUsage] = useState<UsageResponse | null>(null)
  const [killSwitch, setKillSwitch] = useState<KillSwitchResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [actionError, setActionError] = useState<unknown>(null)
  const [actionLoading, setActionLoading] = useState(false)

  const fetchData = async () => {
    try {
      setError(null)
      setLoading(true)
      const [usageData, killSwitchData] = await Promise.all([getUsage(), getKillSwitch()])
      setUsage(usageData)
      setKillSwitch(killSwitchData)
    } catch (err) {
      setError(err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchData()
  }, [])

  const handleHalt = async () => {
    try {
      setActionError(null)
      setActionLoading(true)
      const result = await haltKillSwitch()
      setKillSwitch(result)
    } catch (err) {
      setActionError(err)
    } finally {
      setActionLoading(false)
    }
  }

  const handleResume = async () => {
    try {
      setActionError(null)
      setActionLoading(true)
      const result = await resumeKillSwitch()
      setKillSwitch(result)
    } catch (err) {
      setActionError(err)
    } finally {
      setActionLoading(false)
    }
  }

  if (loading) {
    return <LoadingState label="Loading dashboard…" />
  }

  if (error) {
    return <ErrorState error={error} />
  }

  if (!usage || !killSwitch) {
    return <ErrorState error={new Error('Failed to load dashboard data')} />
  }

  return (
    <div
      className="grid"
      style={{
        gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))',
        maxWidth: '1200px',
      }}
    >
      {/* 5-Hour Usage Card */}
      <div className="card stack">
        <h2>5-Hour Usage</h2>
        <div className="stack" style={{ gap: '8px' }}>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Input tokens:</span>
            <strong>{usage.five_hour.input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cache write:</span>
            <strong>{usage.five_hour.cache_creation_input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cache read:</span>
            <strong>{usage.five_hour.cache_read_input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Output tokens:</span>
            <strong>{usage.five_hour.output_tokens}</strong>
          </div>
          <hr style={{ margin: '8px 0', border: 'none', borderTop: '1px solid var(--border)' }} />
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cost:</span>
            <strong>{formatCurrency(usage.five_hour.cost_usd)}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Entries:</span>
            <strong>{usage.five_hour.entry_count}</strong>
          </div>
        </div>
        <p className="muted" style={{ fontSize: '12px', margin: '4px 0 0 0' }}>
          tokenwarden's tracked spend in this window
        </p>
      </div>

      {/* 7-Day Usage Card */}
      <div className="card stack">
        <h2>7-Day Usage</h2>
        <div className="stack" style={{ gap: '8px' }}>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Input tokens:</span>
            <strong>{usage.seven_day.input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cache write:</span>
            <strong>{usage.seven_day.cache_creation_input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cache read:</span>
            <strong>{usage.seven_day.cache_read_input_tokens}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Output tokens:</span>
            <strong>{usage.seven_day.output_tokens}</strong>
          </div>
          <hr style={{ margin: '8px 0', border: 'none', borderTop: '1px solid var(--border)' }} />
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Cost:</span>
            <strong>{formatCurrency(usage.seven_day.cost_usd)}</strong>
          </div>
          <div className="row" style={{ justifyContent: 'space-between' }}>
            <span>Entries:</span>
            <strong>{usage.seven_day.entry_count}</strong>
          </div>
        </div>
        <p className="muted" style={{ fontSize: '12px', margin: '4px 0 0 0' }}>
          tokenwarden's tracked spend in this window
        </p>
      </div>

      {/* Ground Truth Card */}
      <div className="card stack">
        <h2>Ground Truth</h2>
        {usage.ground_truth ? (
          <div className="stack" style={{ gap: '12px' }}>
            <div>
              <strong>5-Hour Window</strong>
              <div style={{ marginTop: '6px' }}>
                <div className="row" style={{ justifyContent: 'space-between' }}>
                  <span>Usage:</span>
                  <strong>{usage.ground_truth.five_hour.used_percentage.toFixed(1)}%</strong>
                </div>
                <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>
                  Resets: {formatDateTime(usage.ground_truth.five_hour.resets_at)}
                </div>
              </div>
            </div>
            <hr style={{ margin: '4px 0', border: 'none', borderTop: '1px solid var(--border)' }} />
            <div>
              <strong>7-Day Window</strong>
              <div style={{ marginTop: '6px' }}>
                <div className="row" style={{ justifyContent: 'space-between' }}>
                  <span>Usage:</span>
                  <strong>{usage.ground_truth.seven_day.used_percentage.toFixed(1)}%</strong>
                </div>
                <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>
                  Resets: {formatDateTime(usage.ground_truth.seven_day.resets_at)}
                </div>
              </div>
            </div>
            <hr style={{ margin: '4px 0', border: 'none', borderTop: '1px solid var(--border)' }} />
            <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>
              Observed {formatTimeAgo(usage.ground_truth.age_seconds)} ({usage.ground_truth.observed_at})
            </div>
          </div>
        ) : (
          <p className="muted">No ground truth reading yet</p>
        )}
      </div>

      {/* Calibration Card */}
      <div className="card stack">
        <h2>Calibration</h2>
        <div className="stack" style={{ gap: '12px' }}>
          <div>
            <div className="row" style={{ justifyContent: 'space-between', gap: '12px' }}>
              <div>
                <strong>5-Hour</strong>
                <div style={{ marginTop: '4px' }}>
                  {usage.five_hour_calibration.insufficient ? (
                    <span style={{ color: 'var(--warning)' }}>N/A</span>
                  ) : (
                    <span>{usage.five_hour_calibration.tokens_per_percent.toFixed(2)} tokens/%</span>
                  )}
                </div>
              </div>
              <div style={{ textAlign: 'right' }}>
                <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>
                  {usage.five_hour_calibration.samples} sample{usage.five_hour_calibration.samples !== 1 ? 's' : ''}
                </div>
                {usage.five_hour_calibration.insufficient && (
                  <div
                    style={{
                      fontSize: '11px',
                      color: 'var(--warning)',
                      marginTop: '2px',
                    }}
                  >
                    insufficient data
                  </div>
                )}
              </div>
            </div>
          </div>
          <hr style={{ margin: '0', border: 'none', borderTop: '1px solid var(--border)' }} />
          <div>
            <div className="row" style={{ justifyContent: 'space-between', gap: '12px' }}>
              <div>
                <strong>7-Day</strong>
                <div style={{ marginTop: '4px' }}>
                  {usage.seven_day_calibration.insufficient ? (
                    <span style={{ color: 'var(--warning)' }}>N/A</span>
                  ) : (
                    <span>{usage.seven_day_calibration.tokens_per_percent.toFixed(2)} tokens/%</span>
                  )}
                </div>
              </div>
              <div style={{ textAlign: 'right' }}>
                <div style={{ fontSize: '12px', color: 'var(--fg-muted)' }}>
                  {usage.seven_day_calibration.samples} sample{usage.seven_day_calibration.samples !== 1 ? 's' : ''}
                </div>
                {usage.seven_day_calibration.insufficient && (
                  <div
                    style={{
                      fontSize: '11px',
                      color: 'var(--warning)',
                      marginTop: '2px',
                    }}
                  >
                    insufficient data
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Kill Switch Card */}
      <div className="card stack">
        <h2>Kill Switch</h2>
        <div
          className="row"
          style={{
            padding: '8px 12px',
            borderRadius: '6px',
            backgroundColor: killSwitch.halted ? 'rgba(209, 55, 63, 0.1)' : 'rgba(47, 158, 68, 0.1)',
            marginBottom: '8px',
          }}
        >
          <span style={{ fontWeight: '600', flex: 1 }}>Status:</span>
          <span
            style={{
              color: killSwitch.halted ? 'var(--danger)' : 'var(--success)',
              fontWeight: '600',
            }}
          >
            {killSwitch.halted ? 'Halted' : 'Running'}
          </span>
        </div>

        <button
          onClick={killSwitch.halted ? handleResume : handleHalt}
          disabled={actionLoading}
          className={killSwitch.halted ? 'primary' : 'danger'}
        >
          {actionLoading ? 'Loading…' : killSwitch.halted ? 'Resume' : 'Halt'}
        </button>

        {actionError != null && <ErrorState error={actionError} />}
      </div>
    </div>
  )
}
