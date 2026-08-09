import { useState } from 'react'
import { createJob } from '../api/client'
import type { CreateJobRequest, JobKind } from '../api/types'
import ErrorState from '../components/ErrorState'
import { navigate, jobDetailPath } from '../router'

const JOB_KINDS: JobKind[] = ['research', 'plan', 'code', 'review', 'freeform']

// Short model aliases the `claude` CLI resolves itself (to whatever the
// latest point release is) — tokenwarden never hardcodes a specific
// dated model id, so this list doesn't need to track releases.
const MODELS = [
  { value: '', label: 'Default' },
  { value: 'opus', label: 'Opus' },
  { value: 'sonnet', label: 'Sonnet' },
  { value: 'haiku', label: 'Haiku' },
]

const EFFORTS = [
  { value: '', label: 'Default' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'Extra high' },
  { value: 'max', label: 'Max' },
]

// This is the landing page: a prompt composer that looks like a normal
// agent chat session, except submitting doesn't run anything inline — it
// queues a job for the daemon's dispatch loop (or a manual dispatch) to
// pick up in the background. See internal/scheduler and
// dispatch.DispatchOne for what happens after this.
export default function CreateJob() {
  const [kind, setKind] = useState<JobKind>('research')
  const [prompt, setPrompt] = useState('')
  const [workspace, setWorkspace] = useState('')
  const [model, setModel] = useState('')
  const [effort, setEffort] = useState('')
  const [priority, setPriority] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!prompt.trim()) {
      setError(new Error('Prompt is required'))
      return
    }

    const req: CreateJobRequest = {
      kind,
      prompt,
      priority,
      ...(workspace && { workspace }),
      ...(model && { model }),
      ...(effort && { effort }),
    }

    try {
      setLoading(true)
      setError(null)
      const created = await createJob(req)
      navigate(jobDetailPath(created.id))
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      className="stack"
      style={{ maxWidth: '760px', margin: '0 auto', paddingTop: '8vh', gap: '20px' }}
    >
      <div className="stack" style={{ gap: '4px', textAlign: 'center' }}>
        <h1 style={{ margin: 0 }}>What should Claude work on?</h1>
        <p className="muted" style={{ margin: 0 }}>
          Queue it here and it runs in the background, paced against your budget — not in this
          window.
        </p>
      </div>

      {error && <ErrorState error={error} />}

      <form onSubmit={handleSubmit} className="stack" style={{ gap: '12px' }}>
        <div
          className="card"
          style={{ padding: 0, overflow: 'hidden', border: '1px solid var(--border)' }}
        >
          <textarea
            id="prompt"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the task — as much context as you'd give in a live session…"
            required
            autoFocus
            rows={8}
            style={{
              width: '100%',
              border: 'none',
              borderRadius: 0,
              resize: 'vertical',
              fontSize: '15px',
              padding: '16px',
            }}
          />

          <div
            className="row"
            style={{
              justifyContent: 'space-between',
              flexWrap: 'wrap',
              gap: '8px',
              padding: '10px 12px',
              borderTop: '1px solid var(--border)',
              background: 'var(--bg)',
            }}
          >
            <div className="row" style={{ flexWrap: 'wrap', gap: '8px' }}>
              <select
                aria-label="Kind"
                value={kind}
                onChange={(e) => setKind(e.target.value as JobKind)}
              >
                {JOB_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>

              <select aria-label="Model" value={model} onChange={(e) => setModel(e.target.value)}>
                {MODELS.map((m) => (
                  <option key={m.value} value={m.value}>
                    {m.value ? m.label : 'Model: default'}
                  </option>
                ))}
              </select>

              <select
                aria-label="Effort"
                value={effort}
                onChange={(e) => setEffort(e.target.value)}
              >
                {EFFORTS.map((ef) => (
                  <option key={ef.value} value={ef.value}>
                    {ef.value ? ef.label : 'Effort: default'}
                  </option>
                ))}
              </select>

              <input
                aria-label="Priority"
                type="number"
                value={priority}
                onChange={(e) => setPriority(Number(e.target.value))}
                title="Priority (higher runs first)"
                style={{ width: '70px' }}
              />

              <input
                aria-label="Workspace"
                type="text"
                value={workspace}
                onChange={(e) => setWorkspace(e.target.value)}
                placeholder="Workspace (optional)"
                style={{ width: '160px' }}
              />
            </div>

            <button type="submit" className="primary" disabled={loading || !prompt.trim()}>
              {loading ? 'Queuing…' : 'Queue job'}
            </button>
          </div>
        </div>
      </form>
    </div>
  )
}
