export default function ErrorState({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : String(error)
  return (
    <p style={{ color: 'var(--danger)' }}>
      Error: {message}
    </p>
  )
}
