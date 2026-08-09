import { useState } from 'react'
import { createJob } from '../api/client'
import type { CreateJobRequest, JobKind } from '../api/types'
import ErrorState from '../components/ErrorState'
import { navigate, jobDetailPath } from '../router'

const JOB_KINDS: JobKind[] = ['research', 'plan', 'code', 'review', 'freeform']

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
    <div className="stack">
      <h1>New Job</h1>

      {error && <ErrorState error={error} />}

      <form onSubmit={handleSubmit} className="stack" style={{ maxWidth: '600px' }}>
        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="kind" style={{ fontSize: '12px', fontWeight: 600 }}>
            Kind *
          </label>
          <select
            id="kind"
            value={kind}
            onChange={(e) => setKind(e.target.value as JobKind)}
            required
          >
            {JOB_KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </div>

        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="prompt" style={{ fontSize: '12px', fontWeight: 600 }}>
            Prompt *
          </label>
          <textarea
            id="prompt"
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Enter the job prompt..."
            required
            rows={6}
            style={{ fontFamily: 'monospace', fontSize: '13px' }}
          />
          {!prompt.trim() && (
            <p style={{ fontSize: '12px', color: 'var(--danger)', margin: '0' }}>
              Prompt is required
            </p>
          )}
        </div>

        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="workspace" style={{ fontSize: '12px', fontWeight: 600 }}>
            Workspace
          </label>
          <input
            id="workspace"
            type="text"
            value={workspace}
            onChange={(e) => setWorkspace(e.target.value)}
            placeholder="Optional workspace name"
          />
        </div>

        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="model" style={{ fontSize: '12px', fontWeight: 600 }}>
            Model
          </label>
          <input
            id="model"
            type="text"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="e.g., claude-opus-4"
          />
        </div>

        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="effort" style={{ fontSize: '12px', fontWeight: 600 }}>
            Effort
          </label>
          <input
            id="effort"
            type="text"
            value={effort}
            onChange={(e) => setEffort(e.target.value)}
            placeholder="e.g., thorough"
          />
        </div>

        <div className="stack" style={{ gap: '8px' }}>
          <label htmlFor="priority" style={{ fontSize: '12px', fontWeight: 600 }}>
            Priority
          </label>
          <input
            id="priority"
            type="number"
            value={priority}
            onChange={(e) => setPriority(Number(e.target.value))}
            placeholder="0"
          />
        </div>

        <button
          type="submit"
          className="primary"
          disabled={loading || !prompt.trim()}
          style={{ alignSelf: 'flex-start' }}
        >
          {loading ? 'Creating…' : 'Create Job'}
        </button>
      </form>
    </div>
  )
}
