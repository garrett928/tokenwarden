import type {
  CreateJobRequest,
  ErrorResponse,
  JobResponse,
  KillSwitchResponse,
  SchedulerConfigResponse,
  UpdateSchedulerConfigRequest,
  UsageResponse,
} from './types'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as ErrorResponse
      if (body.error) message = body.error
    } catch {
      // body wasn't JSON — fall back to statusText
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export function getUsage(): Promise<UsageResponse> {
  return request('/api/usage')
}

export function getKillSwitch(): Promise<KillSwitchResponse> {
  return request('/api/kill-switch')
}

export function haltKillSwitch(): Promise<KillSwitchResponse> {
  return request('/api/kill-switch/halt', { method: 'POST' })
}

export function resumeKillSwitch(): Promise<KillSwitchResponse> {
  return request('/api/kill-switch/resume', { method: 'POST' })
}

export function listJobs(statuses?: string[]): Promise<JobResponse[]> {
  const params = new URLSearchParams()
  for (const s of statuses ?? []) params.append('status', s)
  const qs = params.toString()
  return request(`/api/jobs${qs ? `?${qs}` : ''}`)
}

export function getJob(id: string): Promise<JobResponse> {
  return request(`/api/jobs/${encodeURIComponent(id)}`)
}

export function createJob(req: CreateJobRequest): Promise<JobResponse> {
  return request('/api/jobs', { method: 'POST', body: JSON.stringify(req) })
}

export function cancelJob(id: string): Promise<JobResponse> {
  return request(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function dispatchJob(id: string): Promise<JobResponse> {
  return request(`/api/jobs/${encodeURIComponent(id)}/dispatch`, { method: 'POST' })
}

export function getSchedulerConfig(): Promise<SchedulerConfigResponse> {
  return request('/api/scheduler/config')
}

export function updateSchedulerConfig(
  req: UpdateSchedulerConfigRequest,
): Promise<SchedulerConfigResponse> {
  return request('/api/scheduler/config', { method: 'PUT', body: JSON.stringify(req) })
}
