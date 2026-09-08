export type Stance = "agent" | "plan" | "auto"

const STANCES: Stance[] = ["agent", "plan", "auto"]

export function cycleStance(current: Stance, delta: 1 | -1): Stance {
  const i = Math.max(0, STANCES.indexOf(current))
  return STANCES[(i + delta + STANCES.length) % STANCES.length]
}

export type Session = {
  session_id: string
  state: string
  version: number
  created_at: string
  updated_at: string
  title?: string
  latest_task_id?: string
  latest_task_state?: string
  task_count: number
  preferred_model?: string
  workspace?: string
  permission_stance?: string
}

export type Task = {
  task_id: string
  session_id?: string
  title: string
  objective: string
  state: string
  execution_mode: string
  version: number
  created_at: string
  updated_at: string
}

export type TranscriptToolCall = {
  id: string
  name: string
  arguments: string
}

export type TranscriptMessage = {
  id: string
  session_id?: string
  task_id?: string
  run_id?: string
  position: number
  role: string
  content: string
  thinking?: string
  tool_call_id?: string
  tool_calls?: TranscriptToolCall[]
  record_type?: string
  created_at: string
}

export type SessionTodo = {
  id: string
  content: string
  status: string
  position: number
  updated_at?: string
}

export type Permission = {
  permission_id: string
  session_id?: string
  task_id: string
  run_id: string
  tool_call_id: string
  tool_name: string
  capability?: string
  path?: string
  command?: string
  command_args?: string[]
  network_domain?: string
  risk?: string
  state: string
  grant_id?: string
  decision?: string
  created_at: string
  decided_at?: string
  suggested_decision?: string
  suggested_reason?: string
  extra_root?: boolean
}

export type UserQuestionOption = {
  label: string
  description?: string
}

export type UserQuestionItem = {
  id: string
  question: string
  header?: string
  options?: UserQuestionOption[]
  multi_select?: boolean
}

export type UserQuestion = {
  question_id: string
  session_id?: string
  task_id?: string
  run_id?: string
  questions: UserQuestionItem[]
  state: string
  answers?: Record<string, string[]>
  created_at: string
}

export type ModelConfig = {
  model: string
  models: string[]
  context_window?: number
  ready: boolean
  error?: string
}

export type MCPStatus = {
  enabled: boolean
  total: number
  ok: number
  error: number
  tools: number
}

export type ChatCommand = {
  id: string
  description?: string
  template: string
}

export type Skill = {
  id: string
  name: string
  description: string
  source: string
  draft?: boolean
  last_used_at?: string
  archived_at?: string
}

export type SkillEvent = {
  event_id: string
  skill_id: string
  action: string
  actor?: string
  path?: string
  content_hash?: string
  created_at: string
}

export type MemoryEntry = {
  entry_id: string
  session_id?: string
  content: string
  source: string
  tags?: string[]
  kind?: string
  priority?: number
  expires_at?: string
  created_at: string
  updated_at?: string
  archived_at?: string
}

export type Job = {
  id: string
  name: string
  session_id: string
  task_title: string
  task_objective: string
  execution_mode: string
  skill_ids?: string[]
  model_ref?: string
  interval_seconds: number
  next_run_at: string
  timeout_seconds: number
  status: string
  created_at: string
  updated_at: string
}

export type TaskContext = {
  task_id: string
  session_id: string
  model: string
  context_window: number
  usable_tokens: number
  last_prompt_tokens: number
  estimate_tokens: number
  pressure: number
  compacted: boolean
  updated_at: string
}

export type Envelope = {
  event_id: string
  event_type: string
  aggregate_type: string
  aggregate_id: string
  sequence: number
  payload?: unknown
}

export type ModelStreamEvent = {
  seq: number
  session_id?: string
  task_id?: string
  run_id?: string
  parent_run_id?: string
  event: {
    type: string
    content_delta?: string
    thinking_delta?: string
    tool_call?: { id?: string; name?: string; arguments?: string }
  }
}

export type SubmitResult = {
  task: Task
  run_id?: string
  plan_id?: string
}

export type Chip = {
  id: string
  mention: string
  abs: string
  outside: boolean
  kind: "file" | "image" | "video"
}

export type LiveTool = {
  id: string
  name: string
  preview: string
}

export type LiveState = {
  content: string
  thinking: string
  tools: LiveTool[]
  runId: string
}

export type PanelDot = "none" | "permission" | "done"
