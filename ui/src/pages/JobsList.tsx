import { useEffect, useState } from 'react'
import { listJobs } from '../api/client'
import type { JobResponse, JobStatus } from '../api/types'
import StatusBadge from '../components/StatusBadge'
import LoadingState from '../components/LoadingState'
import ErrorState from '../components/ErrorState'
import { navigate, jobDetailPath } from '../router'

const ALL_STATUSES: JobStatus[] = [
  'queued',
  'blocked',
  'running',
  'paused_budget',
  'deferred_oversized',
  'promoted',
  'succeeded',
  'failed',
  'cancelled',
]

export default function JobsList() {
  const [jobs, setJobs] = useState<JobResponse[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)
  const [selectedStatuses, setSelectedStatuses] = useState<JobStatus[]>([])

  const fetchJobs = async (statuses: JobStatus[]) => {
    try {
      setLoading(true)
      setError(null)
      const result = await listJobs(statuses.length > 0 ? statuses : undefined)
      setJobs(result || [])
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchJobs(selectedStatuses)
  }, [selectedStatuses])

  const toggleStatus = (status: JobStatus) => {
    setSelectedStatuses((prev) =>
      prev.includes(status) ? prev.filter((s) => s !== status) : [...prev, status]
    )
  }

  const formatDate = (isoString: string) => {
    return new Date(isoString).toLocaleString()
  }

  return (
    <div className="stack">
      <div className="row">
        <h1 style={{ margin: 0, flex: 1 }}>Jobs</h1>
        <button className="primary" onClick={() => navigate('#/jobs/new')}>
          New Job
        </button>
      </div>

      <div className="stack" style={{ gap: '8px' }}>
        <label style={{ fontSize: '12px', fontWeight: 600, textTransform: 'uppercase', color: 'var(--fg-muted)' }}>
          Filter by Status
        </label>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px' }}>
          {ALL_STATUSES.map((status) => (
            <button
              key={status}
              onClick={() => toggleStatus(status)}
              style={{
                padding: '4px 12px',
                fontSize: '12px',
                fontWeight: 500,
                backgroundColor: selectedStatuses.includes(status) ? 'var(--accent)' : 'var(--bg-alt)',
                color: selectedStatuses.includes(status) ? 'var(--accent-fg)' : 'var(--fg)',
                borderColor: selectedStatuses.includes(status) ? 'var(--accent)' : 'var(--border)',
              }}
            >
              {status}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <LoadingState label="Loading jobs…" />
      ) : error ? (
        <ErrorState error={error} />
      ) : jobs.length === 0 ? (
        <p className="muted">No jobs yet — create one to get started</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Kind</th>
                <th>Status</th>
                <th>Priority</th>
                <th>Created</th>
                <th>Updated</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <tr
                  key={job.id}
                  onClick={() => navigate(jobDetailPath(job.id))}
                  style={{ cursor: 'pointer' }}
                >
                  <td title={job.id}>{job.id.slice(0, 8)}</td>
                  <td>{job.kind}</td>
                  <td>
                    <StatusBadge status={job.status} />
                  </td>
                  <td>{job.priority}</td>
                  <td>{formatDate(job.created_at)}</td>
                  <td>{formatDate(job.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
