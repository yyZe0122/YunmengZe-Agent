import type {
  ChatCommand,
  Chip,
  Job,
  LiveState,
  MCPStatus,
  MemoryEntry,
  ModelConfig,
  Permission,
  Session,
  SessionTodo,
  Skill,
  SkillEvent,
  Stance,
  Task,
  TaskContext,
  TranscriptMessage,
  UserQuestion,
} from "../types"

export type HostToWebview =
  | { type: "snapshot"; payload: Snapshot }
  | { type: "sessions"; sessions: Session[] }
  | { type: "transcript"; messages: TranscriptMessage[] }
  | { type: "live"; live: LiveState }
  | { type: "permissions"; permissions: Permission[] }
  | { type: "questions"; questions: UserQuestion[] }
  | { type: "todos"; todos: SessionTodo[] }
  | { type: "context"; context?: TaskContext }
  | { type: "model"; model: ModelConfig; sessionModel: string }
  | { type: "status"; text: string; error?: boolean }
  | { type: "chips"; chips: Chip[] }
  | { type: "insert"; text: string }
  | { type: "focus" }
  | { type: "files"; files: { mention: string; relative: string }[] }
  | { type: "slashResult"; kind: string; body: string }
  | { type: "skills"; skills: Skill[]; selected: string[] }
  | { type: "memory"; entries: MemoryEntry[] }
  | { type: "jobs"; jobs: Job[] }
  | { type: "commands"; commands: ChatCommand[] }
  | { type: "journey"; body: string }
  | { type: "mcp"; mcp: MCPStatus }

export type Snapshot = {
  sessionId: string
  title: string
  stance: Stance
  running: boolean
  task?: Task
  messages: TranscriptMessage[]
  live: LiveState
  permissions: Permission[]
  questions: UserQuestion[]
  todos: SessionTodo[]
  chips: Chip[]
  model: ModelConfig
  sessionModel: string
  context?: TaskContext
  skills: Skill[]
  selectedSkills: string[]
  commands: ChatCommand[]
  status: string
  statusError?: boolean
  version?: string
}

export type WebviewToHost =
  | { type: "ready" }
  | { type: "send"; text: string }
  | { type: "stop" }
  | { type: "stance"; stance: Stance }
  | { type: "preferModel"; model: string }
  | { type: "setMainModel"; model: string }
  | { type: "decide"; permissionId: string; decision: string }
  | { type: "answer"; questionId: string; answers: Record<string, string[]> }
  | { type: "dismiss"; questionId: string }
  | { type: "slash"; line: string }
  | { type: "compact"; focus: string }
  | { type: "undo" }
  | { type: "edit"; rewindFiles: boolean }
  | { type: "retry" }
  | { type: "newSession" }
  | { type: "pickFiles" }
  | { type: "searchFiles"; query: string }
  | { type: "drop"; uris: string[]; paths: string[] }
  | { type: "removeChip"; id: string }
  | { type: "openPath"; path: string }
  | { type: "toggleSkill"; id: string }
  | { type: "skillAction"; action: "apply" | "reject"; id: string }
  | { type: "memoryAction"; action: "refresh" | "forget" | "promote"; id?: string }
  | { type: "createJob"; intervalSeconds: number; objective: string }
  | { type: "jobAction"; id: string; action: "pause" | "resume" | "cancel" }
