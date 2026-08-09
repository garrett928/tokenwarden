import { useEffect, useState } from 'react'
import type { JobResponse } from '../api/types'
import { getJob, cancelJob, dispatchJob } from '../api/client'
import LoadingState from '../components/LoadingState'
import ErrorState from '../components/ErrorState'
import StatusBadge from '../components/StatusBadge'

export default function JobDetail({ id }: { id: string }) {
  const [job, setJob] = useState<JobResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const [actionError, setActionError] = useState<Error | null>(null)
  const [cancelling, setCancelling] = useState(false)
  const [dispatching, setDispatching] = useState(false)

  // Fetch job on mount and when id changes
  useEffect(() => {
    const fetchJob = async () => {
      setLoading(true)
      setError(null)
      setActionError(null)
      try {
        const data = await getJob(id)
        setJob(data)
      } catch (err) {
        setError(err instanceof Error ? err : new Error(String(err)))
      } finally {
        setLoading(false)
      }
    }
    fetchJob()
  }, [id])

  const handleRefresh = async () => {
    setLoading(true)
    setError(null)
    setActionError(null)
    try {
      const data = await getJob(id)
      setJob(data)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setLoading(false)
    }
  }

  const handleCancel = async () => {
    if (!job) return
    setCancelling(true)
    setActionError(null)
    try {
      const updated = await cancelJob(id)
      setJob(updated)
    } catch (err) {
      setActionError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setCancelling(false)
    }
  }

  const handleDispatch = async () => {
    if (!job) return
    setDispatching(true)
    setActionError(null)
    try {
      const updated = await dispatchJob(id)
      setJob(updated)
    } catch (err) {
      setActionError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setDispatching(false)
    }
  }

  // Button gating logic
  const isTerminal = ['succeeded', 'failed', 'cancelled'].includes(job?.status ?? '')
  const canCancel = !isTerminal
  const canDispatch = job?.status === 'queued' || job?.status === 'paused_budget'

  if (loading && !job) {
    return <LoadingState label="Loading job…" />
  }

  if (error && !job) {
    return <ErrorState error={error} />
  }

  if (!job) {
    return <ErrorState error={new Error('No job data')} />
  }

  return (
    <div className="stack">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h1>Job {id}</h1>
        <div className="row">
          <button onClick={handleRefresh} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
          <button
            onClick={handleCancel}
            className="danger"
            disabled={!canCancel || cancelling}
          >
            {cancelling ? 'Cancelling…' : 'Cancel'}
          </button>
          <button
            onClick={handleDispatch}
            className="primary"
            disabled={!canDispatch || dispatching}
          >
            {dispatching ? 'Dispatching…' : 'Dispatch'}
          </button>
        </div>
      </div>

      {actionError && <ErrorState error={actionError} />}

      <div className="card">
        <div className="stack">
          {/* Basic Info Section */}
          <div>
            <h2 style={{ margin: '0 0 12px 0' }}>Job Info</h2>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  ID
                </div>
                <div style={{ marginTop: '4px', wordBreak: 'break-all' }}>{job.id}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Status
                </div>
                <div style={{ marginTop: '4px' }}>
                  <StatusBadge status={job.status} />
                </div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Kind
                </div>
                <div style={{ marginTop: '4px' }}>{job.kind}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Priority
                </div>
                <div style={{ marginTop: '4px' }}>{job.priority}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Workspace
                </div>
                <div style={{ marginTop: '4px' }}>{job.workspace}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Model
                </div>
                <div style={{ marginTop: '4px' }}>{job.model}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Effort
                </div>
                <div style={{ marginTop: '4px' }}>{job.effort}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Resumable
                </div>
                <div style={{ marginTop: '4px' }}>{job.resumable ? 'Yes' : 'No'}</div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Cost
                </div>
                <div style={{ marginTop: '4px' }}>
                  {job.cost_usd !== undefined ? `$${job.cost_usd.toFixed(4)}` : '—'}
                </div>
              </div>
            </div>
          </div>

          {/* Prompt Section */}
          <div>
            <div className="muted" style={{ fontSize: '12px', fontWeight: 600, marginBottom: '8px' }}>
              Prompt
            </div>
            <pre>{job.prompt}</pre>
          </div>

          {/* Result Section (if present) */}
          {job.result && (
            <div>
              <div className="muted" style={{ fontSize: '12px', fontWeight: 600, marginBottom: '8px' }}>
                Result
              </div>
              <pre>{job.result}</pre>
            </div>
          )}

          {/* Failure Reason (if present) */}
          {job.failure_reason && (
            <div>
              <div style={{ color: 'var(--danger)', fontWeight: 600, marginBottom: '8px' }}>
                Failure Reason
              </div>
              <div style={{ color: 'var(--danger)' }}>{job.failure_reason}</div>
            </div>
          )}

          {/* Optional Fields Section */}
          {(job.earliest_at ||
            job.deadline_at ||
            job.max_budget_usd ||
            job.session_id ||
            (job.depends_on && job.depends_on.length > 0)) && (
            <div>
              <h3 style={{ margin: '0 0 12px 0' }}>Scheduling & Constraints</h3>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
                {job.earliest_at && (
                  <div>
                    <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                      Earliest At
                    </div>
                    <div style={{ marginTop: '4px' }}>
                      {new Date(job.earliest_at).toLocaleString()}
                    </div>
                  </div>
                )}
                {job.deadline_at && (
                  <div>
                    <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                      Deadline At
                    </div>
                    <div style={{ marginTop: '4px' }}>
                      {new Date(job.deadline_at).toLocaleString()}
                    </div>
                  </div>
                )}
                {job.max_budget_usd !== undefined && (
                  <div>
                    <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                      Max Budget
                    </div>
                    <div style={{ marginTop: '4px' }}>${job.max_budget_usd.toFixed(2)}</div>
                  </div>
                )}
                {job.session_id && (
                  <div>
                    <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                      Session ID
                    </div>
                    <div style={{ marginTop: '4px', wordBreak: 'break-all' }}>{job.session_id}</div>
                  </div>
                )}
                {job.depends_on && job.depends_on.length > 0 && (
                  <div>
                    <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                      Depends On
                    </div>
                    <div style={{ marginTop: '4px' }}>
                      {job.depends_on.map((depId, i) => (
                        <div key={i}>{depId}</div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* Timestamps Section */}
          <div>
            <h3 style={{ margin: '0 0 12px 0' }}>Timestamps</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Created At
                </div>
                <div style={{ marginTop: '4px' }}>
                  {new Date(job.created_at).toLocaleString()}
                </div>
              </div>
              <div>
                <div className="muted" style={{ fontSize: '12px', fontWeight: 600 }}>
                  Updated At
                </div>
                <div style={{ marginTop: '4px' }}>
                  {new Date(job.updated_at).toLocaleString()}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
