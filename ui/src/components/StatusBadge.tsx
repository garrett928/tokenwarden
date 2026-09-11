const COLORS: Record<string, string> = {
  queued: 'var(--fg-muted)',
  blocked: 'var(--warning)',
  running: 'var(--accent)',
  paused_budget: 'var(--warning)',
  deferred_oversized: 'var(--warning)',
  promoted: 'var(--success)',
  succeeded: 'var(--success)',
  failed: 'var(--danger)',
  cancelled: 'var(--fg-muted)',
}

export default function StatusBadge({ status }: { status: string }) {
  const color = COLORS[status] ?? 'var(--fg-muted)'
  return (
    <span
      style={{
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 999,
        fontSize: 12,
        fontWeight: 600,
        color,
        border: `1px solid ${color}`,
      }}
    >
      {status}
    </span>
  )
}
