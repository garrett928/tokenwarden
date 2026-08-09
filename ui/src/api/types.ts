// TypeScript mirrors of the Go DTOs in internal/api. Field names and
// optionality match the JSON tags exactly — see the referenced Go files
// before changing anything here, since a mismatch fails silently (extra
// fields are just dropped, missing ones read as undefined).

// internal/api/dto.go
export interface Attachment {
  path: string
  name?: string
}

export type JobKind = 'research' | 'plan' | 'code' | 'review' | 'freeform'

export type JobStatus =
  | 'queued'
  | 'blocked'
  | 'running'
  | 'paused_budget'
  | 'deferred_oversized'
  | 'succeeded'
  | 'failed'
  | 'cancelled'

export interface CreateJobRequest {
  kind: JobKind
  prompt: string
  workspace?: string
  model?: string
  effort?: string
  attachments?: Attachment[]
  steps?: string[]
  resumable?: boolean
  priority?: number
  earliest_at?: string
  deadline_at?: string
  max_budget_usd?: number
  depends_on?: string[]
  permission_mode?: string
  allowed_tools?: string[]
  add_dirs?: string[]
  freeform_worktree?: boolean
  json_schema?: string
}

export interface JobResponse {
  id: string
  kind: string
  prompt: string
  workspace: string
  model: string
  effort: string
  attachments: Attachment[]
  steps: string[]
  resumable: boolean
  priority: number
  earliest_at?: string
  deadline_at?: string
  max_budget_usd?: number
  depends_on: string[]
  permission_mode?: string
  allowed_tools?: string[]
  add_dirs?: string[]
  freeform_worktree?: boolean
  json_schema?: string
  session_id?: string
  status: JobStatus
  failure_reason?: string
  result?: string
  cost_usd?: number // sum of ledger entries recorded for this job; absent until it has run
  created_at: string
  updated_at: string
}

// internal/api/usage.go
export interface WindowUsage {
  input_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  output_tokens: number
  cost_usd: number
  entry_count: number
}

export interface CalibrationResponse {
  tokens_per_percent: number
  samples: number
  insufficient: boolean
}

// internal/api/groundtruth.go
export interface RateLimitWindow {
  used_percentage: number
  resets_at: number // unix seconds
}

export interface GroundTruthResponse {
  five_hour: RateLimitWindow
  seven_day: RateLimitWindow
  observed_at: string
  age_seconds: number
}

export interface UsageResponse {
  five_hour: WindowUsage
  seven_day: WindowUsage
  ground_truth?: GroundTruthResponse
  five_hour_calibration: CalibrationResponse
  seven_day_calibration: CalibrationResponse
}

// internal/api/killswitch.go
export interface KillSwitchResponse {
  halted: boolean
}

// internal/api/scheduler.go
export interface TimeBlockDTO {
  days?: number[] // 0=Sunday..6=Saturday (time.Weekday), empty/absent = every day
  start_min: number // minutes since local midnight, [0, 1440)
  end_min: number // end_min <= start_min means the block wraps past midnight
}

export interface SchedulerConfigResponse {
  enabled: boolean
  aggressiveness: number // 0-100
  reserved_blocks: TimeBlockDTO[]
  preferred_windows: TimeBlockDTO[]
  max_budget_usd?: number
  updated_at: number
}

export interface UpdateSchedulerConfigRequest {
  enabled: boolean
  aggressiveness: number
  reserved_blocks: TimeBlockDTO[]
  preferred_windows: TimeBlockDTO[]
  max_budget_usd?: number
}

// internal/api/response.go
export interface ErrorResponse {
  error: string
}
