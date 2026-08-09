export default function LoadingState({ label = 'Loading…' }: { label?: string }) {
  return <p className="muted">{label}</p>
}
