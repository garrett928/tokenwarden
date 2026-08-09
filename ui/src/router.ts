// Minimal hash router. Only 5 routes exist for this phase, so a tiny
// hand-rolled parser is simpler than a router dependency and trivially
// extendable by copying a case.

export type Route =
  | { name: 'dashboard' }
  | { name: 'jobs' }
  | { name: 'job-new' }
  | { name: 'job-detail'; id: string }
  | { name: 'scheduler' }

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '') || '/'
  const parts = path.split('/').filter(Boolean)

  if (parts.length === 0) return { name: 'dashboard' }
  if (parts[0] === 'jobs' && parts.length === 1) return { name: 'jobs' }
  if (parts[0] === 'jobs' && parts[1] === 'new') return { name: 'job-new' }
  if (parts[0] === 'jobs' && parts[1]) return { name: 'job-detail', id: decodeURIComponent(parts[1]) }
  if (parts[0] === 'scheduler') return { name: 'scheduler' }

  return { name: 'dashboard' }
}

export function navigate(hash: string): void {
  window.location.hash = hash
}

export function jobDetailPath(id: string): string {
  return `#/jobs/${encodeURIComponent(id)}`
}
