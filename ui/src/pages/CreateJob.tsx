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

// Exactly internal/runner/safety.go's permissionModeAllowlist — the only
// modes BuildArgs accepts for a freeform job. "bypassPermissions"
// (--dangerously-skip-permissions) is deliberately never offered (FR-SAFE-1).
const PERMISSION_MODES = ['dontAsk', 'plan', 'acceptEdits', 'default']

// A representative set of tools a freeform job might want — not exhaustive,
// just enough to build a real "generic assistant" (web access, a shell)
// without needing to hand-type a tool name. Leaving every box unchecked
// sends no allowed_tools at all, which is what makes freeform + dontAsk
// behave like an ordinary unrestricted `claude -p` session rather than the
// fixed, narrower profiles the other kinds get (research/plan/review are
// permanently read-only + no-tools-beyond-read-only by design, not
// something a job can loosen — see safety.go's profileFor doc comment).
const FREEFORM_TOOLS = ['Read', 'Grep', 'Glob', 'WebFetch', 'WebSearch', 'Bash', 'Write', 'Edit']

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
  const [permissionMode, setPermissionMode] = useState('dontAsk')
  const [allowedTools, setAllowedTools] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const toggleTool = (tool: string) => {
    setAllowedTools((prev) => {
      const next = new Set(prev)
      if (next.has(tool)) next.delete(tool)
      else next.add(tool)
      return next
    })
  }

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
      ...(kind === 'freeform' && { permission_mode: permissionMode }),
      ...(kind === 'freeform' && allowedTools.size > 0 && { allowed_tools: Array.from(allowedTools) }),
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

          {kind === 'freeform' && (
            <div
              className="stack"
              style={{
                gap: '8px',
                padding: '10px 12px',
                borderTop: '1px solid var(--border)',
                background: 'var(--bg)',
              }}
            >
              <p className="muted" style={{ margin: 0, fontSize: '12px' }}>
                Freeform jobs choose their own autonomy posture — unlike research/plan/code/review,
                which run a fixed, non-configurable profile. Leave every tool unchecked for an
                unrestricted, general-purpose agent (equivalent to an ordinary <code>claude -p</code>{' '}
                session); check specific tools to restrict it to just those.
              </p>
              <div className="row" style={{ flexWrap: 'wrap', gap: '12px' }}>
                <label className="row" style={{ gap: '4px' }}>
                  <span style={{ fontSize: '12px' }}>Permission mode</span>
                  <select
                    aria-label="Permission mode"
                    value={permissionMode}
                    onChange={(e) => setPermissionMode(e.target.value)}
                  >
                    {PERMISSION_MODES.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <div className="row" style={{ flexWrap: 'wrap', gap: '10px' }}>
                {FREEFORM_TOOLS.map((tool) => (
                  <label
                    key={tool}
                    className="row"
                    style={{ gap: '4px', fontSize: '12px', cursor: 'pointer' }}
                  >
                    <input
                      type="checkbox"
                      checked={allowedTools.has(tool)}
                      onChange={() => toggleTool(tool)}
                    />
                    {tool}
                  </label>
                ))}
              </div>
            </div>
          )}
        </div>
      </form>
    </div>
  )
}
