// What `rehearse` returns from its local API. Only the fields the window uses.

export type Risk = 'high' | 'medium' | 'low'
export type Phase = 'running' | 'reviewing' | 'deciding' | 'applying' | 'finished'
export type Level = 'read' | 'skim' | 'mechanical'

export interface Identifier {
  id: string
  kind: 'real' | 'canary' | 'secret'
  label: string
  name?: string
}

export interface Review {
  summary: string
  risk: Risk
  concern: string
  source: 'model' | 'rules'
  note?: string
}

export interface Action {
  id: string
  kind: 'git-push' | 'npm-publish' | 'gh' | 'http' | 'file'
  method?: string
  scheme?: string
  host?: string
  path?: string
  query?: string
  request_bytes?: number
  content_type?: string
  command?: string[]
  work_dir?: string
  destination: string
  remote?: string
  subject?: string
  files?: string[]
  commits?: string[]
  secret_files?: string[]
  outside_sources?: string[]
  carries: Identifier[]
  force?: boolean
  deletes?: boolean
  needs_real_answer?: boolean
  provider_door?: boolean
  provenance?: { source_host: string; seconds_before: number }
  file?: { path: string; change: 'added' | 'changed' | 'deleted'; dir?: boolean; files?: number }
  review: Review | null
  decision: '' | 'approved' | 'refused'
  outcome: '' | 'applied' | 'failed' | 'not_applied'
  detail: string
}

export interface Server {
  host: string
  category: 'provider' | 'other'
  requests: number
  flagged: boolean
  received: Identifier[]
}

export interface Claim {
  claim: string
  verdict: string
  command?: string
}

export interface Ledger {
  command: string[] | null
  work_dir: string
  started_at: string
  phase: Phase
  totals: { requests: number; servers: number; flagged_servers: number; held: number }
  servers: Server[]
  actions: Action[]
  outside: { host: string; reads: number; hidden: number }[]
  report: {
    before_input: { watched: boolean; input_seen: boolean; requests: number }
    models: { name: string; requests: number }[]
    tokens: { input: number | null; output: number | null; cache_read: number | null; cache_write: number | null; cache_share: number | null }
    commands_known: boolean
    commands: { text: string; failed: boolean | null }[]
    flags: string[]
    notes: { claims_checked: boolean; claims?: Claim[] } | null
  }
  home?: string
  tool_version: string
  changes: { in_repo: boolean; files: number; added: number; removed: number; problem?: string } | null
  record: { verified: boolean; entries: number; bad_entry: number; unsigned: boolean }
  sensitive: { path: string; reason: string; real: boolean }[]
  packages: { manager: string; name: string; facts: string; blocked: boolean }[]
  replay: boolean
  // what the agent said, or was told, set against what was observed; null
  // until the session is over and when there is nothing to say
  truth?: Truth | null
}

export interface TruthHalf {
  text: string
  // numbers of the log entries it rests on
  entries: number[]
}

export interface Truth {
  form: 'claim' | 'told'
  sentence: string
  said: TruthHalf
  observed: TruthHalf
}

export interface Part {
  id: string
  path: string
  old_path?: string
  status: 'added' | 'deleted' | 'modified' | 'renamed'
  whole: boolean
  binary?: boolean
  line?: number
  added: number
  removed: number
  level: Level
  reason: string
  // what the local model said the change does, once it has read it
  sentence?: string
  // the rules said skim and the model said read
  raised?: boolean
  // the model is going to read it and has not yet
  pending?: boolean
}

export interface Changes {
  captured: boolean
  in_repo: boolean
  problem?: string
  truncated?: boolean
  // the local model is reading the parts now; and what it left unread, once it has finished
  reading?: boolean
  reading_note?: string
  parts: Part[]
}

export interface SessionInfo {
  name: string
  work_dir: string
  project: string
  command: string
  started_at: string
  current: boolean
  replay?: boolean
  // the first words of what the person asked the agent, when the record has them
  title?: string
}

export interface Info {
  version: string
  reviewer: string
}

export interface Entry {
  seq: number
  time: string
  type: string
  data: unknown
  prev: string
  hash: string
}

export interface RecordCheck {
  verified: boolean
  entries: number
  final_hash: string
  signed_at?: string
  problem?: string
  bad_entry?: number
}

/** Everything the window shows about one session. */
export interface Session {
  info: Info
  sessions: SessionInfo[]
  ledger: Ledger
  changes: Changes
  // the text of each part and the entries of the log, when they have been asked for
  texts: Record<string, string>
}

/** A pointer at one entry of one record, and at the part of its raw line that matters. */
export interface EntryRef {
  session: string
  label: string
  seq: number
  hash: string
  mark?: string
}

/** One line of a record exactly as it was stored. */
export interface CaseEntry {
  session: string
  label: string
  seq: number
  time: string
  type: string
  data: string
  prev: string
  hash: string
  problem?: string
}

export interface Custody {
  session: string
  label: string
  kind: string
  source: string
  entries: number
  bad_entries: number[]
  chain: string
  signature: string
  key?: string
  verified: boolean
  command: string
}

export interface Sentence {
  text: string
  refs: EntryRef[]
}

export interface Narrative {
  model: string
  sentences: Sentence[]
  removed: number
  given: EntryRef[]
  note?: string
}

export interface Entity {
  kind: string
  name: string
}

export interface Finding {
  id: string
  kind: string
  // ten words or fewer, one line that supports it, and the full sentence
  headline: string
  support: string
  title: string
  // the one entry that most directly shows it
  key: EntryRef
  severity: number
  sessions: string[]
  statements: { kind: 'Observed' | 'Derived'; text: string; rule?: string; refs: EntryRef[] }[]
  chain: { label: string; rule?: string; refs: EntryRef[] }[]
  extracts?: { ref: EntryRef; source: string; text: string; marked?: string; hidden?: string }[]
  proof?: { encoding: string; encoded: string; value: string; stored_hash?: string; decoded_hash?: string; equal: boolean; text: string; refs: EntryRef[] }
  how: { stage: string; what: string; refs: EntryRef[] }[]
  links?: { from: Entity; to: Entity; via: string; refs: EntryRef[] }[]
  refs: EntryRef[]
  unverified: EntryRef[]
  unverified_note?: string
  record_note?: string
  narrative?: Narrative
}

/** What the rules found in one session or several, with the entries it rests on. */
export interface Case {
  tool_version: string
  rule_set: string
  records: Custody[]
  findings: Finding[]
  kinds: { kind: string; title: string }[]
  limits: string[]
  timeline: { session: string; label: string; items: { seq: number; time: string; type: string; label: string; bad?: boolean; findings: string[] }[] }[]
  overview: { session: string; label: string; cells: string[][] }[]
  entries: CaseEntry[]
  account?: Narrative
  model_note?: string
}

export interface Answer extends Narrative {
  question: string
  not_in_record: boolean
  text?: string
  entries: CaseEntry[]
}
