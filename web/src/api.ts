// The page's only way to reach its data, so the transport (HTTP and
// server-sent events today) can be swapped without touching the components.
// Everything that arrives is checked here before the components see it.

import { fixtureNames, fixtureSession } from './fixtures'
import type { FixtureSession } from './fixtures'

export type Kind = 'real' | 'canary' | 'secret'
export type Category = 'provider' | 'other'
export type Where = 'address' | 'header' | 'body'
export type Phase = 'running' | 'reviewing' | 'deciding' | 'applying' | 'finished'
export type Risk = 'low' | 'medium' | 'high'
export type ActionKind = 'git-push' | 'npm-publish' | 'gh' | 'http'
export type ReviewSource = 'model' | 'rules'
export type Decision = '' | 'approved' | 'refused'
export type Outcome = '' | 'applied' | 'failed' | 'not_applied'
export type EventType =
  | 'session_started'
  | 'request'
  | 'response'
  | 'identifier_seen'
  | 'session_ended'
  | 'action_held'
  | 'action_reviewed'
  | 'action_approved'
  | 'action_refused'
  | 'action_applied'
  | 'action_failed'
  | 'outside_content'
  | 'first_input'
  | 'model_usage'
  | 'agent_action'
  | 'agent_result'
  | 'report_notes'
  | 'commits_created'

const kinds: readonly Kind[] = ['real', 'canary', 'secret']
const categories: readonly Category[] = ['provider', 'other']
const wheres: readonly Where[] = ['address', 'header', 'body']
const phases: readonly Phase[] = ['running', 'reviewing', 'deciding', 'applying', 'finished']
const risks: readonly Risk[] = ['low', 'medium', 'high']
const actionKinds: readonly ActionKind[] = ['git-push', 'npm-publish', 'gh', 'http']
const reviewSources: readonly ReviewSource[] = ['model', 'rules']
const decisions: readonly Decision[] = ['', 'approved', 'refused']
const outcomes: readonly Outcome[] = ['', 'applied', 'failed', 'not_applied']
const eventTypes: readonly EventType[] = [
  'session_started',
  'request',
  'response',
  'identifier_seen',
  'session_ended',
  'action_held',
  'action_reviewed',
  'action_approved',
  'action_refused',
  'action_applied',
  'action_failed',
  'outside_content',
  'first_input',
  'model_usage',
  'agent_action',
  'agent_result',
  'report_notes',
  'commits_created',
]

export interface Identifier {
  id: string
  kind: Kind
  label: string
  value: string
}

export interface Received extends Identifier {
  requests: number
  where: Where[]
}

export interface Server {
  host: string
  category: Category
  requests: number
  flagged: boolean
  received: Received[]
}

export interface Totals {
  requests: number
  servers: number
  identifiers_sent: number
  flagged_servers: number
  held: number
}

export interface OutsideSource {
  host: string
  reads: number
  hidden: number
}

export interface ReportClaim {
  claim: string
  verdict: string
  command: string
}

/** What was observed about the session. A null figure was not stated anywhere. */
export interface Report {
  inputWatched: boolean
  inputSeen: boolean
  requestsBeforeInput: number
  models: { name: string; requests: number }[]
  tokens: { input: number | null; output: number | null; cacheRead: number | null; cacheWrite: number | null; cacheShare: number | null }
  commandsKnown: boolean
  commands: { text: string; failed: boolean | null }[]
  flags: string[]
  // written by the local model; empty when it gave no usable answer
  modelSummary: string
  claimsChecked: boolean
  claims: ReportClaim[]
}

export interface Ledger {
  command: string[]
  phase: Phase
  holding: boolean
  totals: Totals
  watching: Identifier[]
  servers: Server[]
  outside: OutsideSource[]
  report: Report
  // whether the log's chain of hashes recomputes; badEntry is the first
  // entry that does not match
  record: { verified: boolean; badEntry: number }
}

export interface Review {
  summary: string
  risk: Risk
  concern: string
  source: ReviewSource
  note: string
}

export interface HeldAction {
  id: string
  kind: ActionKind
  // the command line or request exactly as the agent issued it, secrets masked
  exact: string
  workDir: string
  destination: string
  subject: string
  size: number
  contentType: string
  files: string[]
  filesOmitted: number
  commits: string[]
  commitsOmitted: number
  secretFiles: string[]
  carries: Identifier[]
  force: boolean
  deletes: boolean
  needsRealAnswer: boolean
  // goes to the model provider under another account, or as a file upload
  providerDoor: boolean
  // where the destination's address was first seen, when that was in
  // content fetched from another server
  provenance: { sourceHost: string; secondsBefore: number } | null
  // servers whose content had been read before this action was issued
  outsideSources: string[]
  review: Review | null
  decision: Decision
  outcome: Outcome
  detail: string
}

export interface Held {
  phase: Phase
  actions: HeldAction[]
}

export interface LogEvent {
  seq: number
  type: EventType
  time: string
  // one line for the timeline: the request or the held action. Empty for
  // events the timeline does not show.
  what: string
}

export interface Subscription {
  close(): void
}

type Fields = Record<string, unknown>

function fields(value: unknown, what: string): Fields {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(`${what} is not an object`)
  }
  return { ...value }
}

function text(from: Fields, key: string, what: string): string {
  const value = from[key]
  if (typeof value !== 'string') {
    throw new Error(`${what}.${key} is not text`)
  }
  return value
}

function count(from: Fields, key: string, what: string): number {
  const value = from[key]
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
    throw new Error(`${what}.${key} is not a count`)
  }
  return value
}

function flag(from: Fields, key: string, what: string): boolean {
  const value = from[key]
  if (typeof value !== 'boolean') {
    throw new Error(`${what}.${key} is not true or false`)
  }
  return value
}

function oneOf<T extends string>(from: Fields, key: string, allowed: readonly T[], what: string): T {
  const value = text(from, key, what)
  const match = allowed.find((option) => option === value)
  if (match === undefined) {
    throw new Error(`${what}.${key} is "${value}", expected one of: ${allowed.join(', ')}`)
  }
  return match
}

function list<T>(from: Fields, key: string, what: string, parse: (item: unknown, what: string) => T): T[] {
  const value: unknown = from[key]
  if (!Array.isArray(value)) {
    throw new Error(`${what}.${key} is not a list`)
  }
  return value.map((item: unknown, index) => parse(item, `${what}.${key}[${index}]`))
}

function word(item: unknown, what: string): string {
  return text({ item }, 'item', what)
}

// The server leaves out fields that are empty, zero or false. These read
// such a field, and still reject one that is present with the wrong type.
function maybeText(from: Fields, key: string, what: string): string {
  return from[key] === undefined ? '' : text(from, key, what)
}

function maybeCount(from: Fields, key: string, what: string): number {
  return from[key] === undefined ? 0 : count(from, key, what)
}

function maybeFlag(from: Fields, key: string, what: string): boolean {
  return from[key] === undefined ? false : flag(from, key, what)
}

function maybeWords(from: Fields, key: string, what: string): string[] {
  return from[key] === undefined || from[key] === null ? [] : list(from, key, what, word)
}

function parseIdentifier(value: unknown, what: string): Identifier {
  const from = fields(value, what)
  return {
    id: text(from, 'id', what),
    kind: oneOf(from, 'kind', kinds, what),
    label: text(from, 'label', what),
    value: text(from, 'value', what),
  }
}

function parseReceived(value: unknown, what: string): Received {
  const from = fields(value, what)
  return {
    ...parseIdentifier(value, what),
    requests: count(from, 'requests', what),
    where: list(from, 'where', what, (item, itemWhat) => oneOf({ item }, 'item', wheres, itemWhat)),
  }
}

function parseServer(value: unknown, what: string): Server {
  const from = fields(value, what)
  return {
    host: text(from, 'host', what),
    category: oneOf(from, 'category', categories, what),
    requests: count(from, 'requests', what),
    flagged: flag(from, 'flagged', what),
    received: list(from, 'received', what, parseReceived),
  }
}

function parseLedger(value: unknown): Ledger {
  const what = 'ledger'
  const from = fields(value, what)
  const totals = fields(from['totals'], 'ledger.totals')
  return {
    // null until the session_started event has been written
    command: from['command'] === null ? [] : list(from, 'command', what, word),
    phase: oneOf(from, 'phase', phases, what),
    holding: flag(from, 'holding', what),
    totals: {
      requests: count(totals, 'requests', 'ledger.totals'),
      servers: count(totals, 'servers', 'ledger.totals'),
      identifiers_sent: count(totals, 'identifiers_sent', 'ledger.totals'),
      flagged_servers: count(totals, 'flagged_servers', 'ledger.totals'),
      held: count(totals, 'held', 'ledger.totals'),
    },
    watching: list(from, 'watching', what, parseIdentifier),
    servers: list(from, 'servers', what, parseServer),
    outside: list(from, 'outside', what, (item, itemWhat) => {
      const o = fields(item, itemWhat)
      return { host: text(o, 'host', itemWhat), reads: count(o, 'reads', itemWhat), hidden: count(o, 'hidden', itemWhat) }
    }),
    report: parseReport(from['report']),
    record: parseRecord(from['record']),
  }
}

function parseRecord(value: unknown): { verified: boolean; badEntry: number } {
  const from = fields(value, 'ledger.record')
  return { verified: flag(from, 'verified', 'ledger.record'), badEntry: count(from, 'bad_entry', 'ledger.record') }
}

function maybeNumber(from: Fields, key: string, what: string): number | null {
  return from[key] === null ? null : count(from, key, what)
}

function parseReport(value: unknown): Report {
  const what = 'ledger.report'
  const from = fields(value, what)
  const before = fields(from['before_input'], `${what}.before_input`)
  const tokens = fields(from['tokens'], `${what}.tokens`)
  const notes = from['notes'] === null ? null : fields(from['notes'], `${what}.notes`)
  return {
    inputWatched: flag(before, 'watched', what),
    inputSeen: flag(before, 'input_seen', what),
    requestsBeforeInput: count(before, 'requests', what),
    models: list(from, 'models', what, (item, itemWhat) => {
      const m = fields(item, itemWhat)
      return { name: text(m, 'name', itemWhat), requests: count(m, 'requests', itemWhat) }
    }),
    tokens: {
      input: maybeNumber(tokens, 'input', what),
      output: maybeNumber(tokens, 'output', what),
      cacheRead: maybeNumber(tokens, 'cache_read', what),
      cacheWrite: maybeNumber(tokens, 'cache_write', what),
      cacheShare: maybeNumber(tokens, 'cache_share', what),
    },
    commandsKnown: flag(from, 'commands_known', what),
    commands: list(from, 'commands', what, (item, itemWhat) => {
      const c = fields(item, itemWhat)
      return { text: text(c, 'text', itemWhat), failed: c['failed'] === null ? null : flag(c, 'failed', itemWhat) }
    }),
    flags: list(from, 'flags', what, word),
    modelSummary: notes === null ? '' : maybeText(notes, 'summary', what),
    claimsChecked: notes === null ? false : flag(notes, 'claims_checked', what),
    claims:
      notes === null || notes['claims'] === undefined
        ? []
        : list(notes, 'claims', what, (item, itemWhat) => {
            const c = fields(item, itemWhat)
            return { claim: text(c, 'claim', itemWhat), verdict: text(c, 'verdict', itemWhat), command: maybeText(c, 'command', itemWhat) }
          }),
  }
}

function parseReview(value: unknown, what: string): Review {
  const from = fields(value, what)
  return {
    summary: text(from, 'summary', what),
    risk: oneOf(from, 'risk', risks, what),
    concern: text(from, 'concern', what),
    source: oneOf(from, 'source', reviewSources, what),
    note: maybeText(from, 'note', what),
  }
}

function parseProvenance(value: unknown, what: string): { sourceHost: string; secondsBefore: number } {
  const from = fields(value, what)
  return { sourceHost: text(from, 'source_host', what), secondsBefore: count(from, 'seconds_before', what) }
}

function parseAction(value: unknown, what: string): HeldAction {
  const from = fields(value, what)
  const kind = oneOf(from, 'kind', actionKinds, what)
  const exact =
    kind === 'http'
      ? `${text(from, 'method', what)} ${text(from, 'host', what)}${text(from, 'path', what)}`
      : list(from, 'command', what, word).join(' ')
  return {
    id: text(from, 'id', what),
    kind,
    exact,
    workDir: maybeText(from, 'work_dir', what),
    destination: text(from, 'destination', what),
    subject: maybeText(from, 'subject', what),
    size: maybeCount(from, kind === 'http' ? 'request_bytes' : 'stdin_bytes', what),
    contentType: maybeText(from, 'content_type', what),
    files: maybeWords(from, 'files', what),
    filesOmitted: maybeCount(from, 'files_omitted', what),
    commits: maybeWords(from, 'commits', what),
    commitsOmitted: maybeCount(from, 'commits_omitted', what),
    secretFiles: maybeWords(from, 'secret_files', what),
    carries: list(from, 'carries', what, parseIdentifier),
    force: maybeFlag(from, 'force', what),
    deletes: maybeFlag(from, 'deletes', what),
    needsRealAnswer: maybeFlag(from, 'needs_real_answer', what),
    providerDoor: maybeFlag(from, 'provider_door', what),
    provenance: from['provenance'] === undefined ? null : parseProvenance(from['provenance'], `${what}.provenance`),
    outsideSources: maybeWords(from, 'outside_sources', what),
    review: from['review'] === null ? null : parseReview(from['review'], `${what}.review`),
    decision: oneOf(from, 'decision', decisions, what),
    outcome: oneOf(from, 'outcome', outcomes, what),
    detail: text(from, 'detail', what),
  }
}

function parseHeld(value: unknown): Held {
  const from = fields(value, 'held')
  return {
    phase: oneOf(from, 'phase', phases, 'held'),
    actions: list(from, 'actions', 'held', parseAction),
  }
}

function parseLogEvent(value: unknown): LogEvent {
  const from = fields(value, 'event')
  const type = oneOf(from, 'type', eventTypes, 'event')
  const data = fields(from['data'], 'event.data')
  let what = ''
  if (type === 'request') {
    what = `${text(data, 'method', 'event.data')} ${text(data, 'host', 'event.data')}${text(data, 'path', 'event.data')}`
  } else if (type === 'action_held') {
    what = `held: ${parseAction({ ...data, carries: [], review: null, decision: '', outcome: '', detail: '' }, 'event.data').exact}`
  }
  return { seq: count(from, 'seq', 'event'), type, time: text(from, 'time', 'event'), what }
}

async function get(path: string, what: string): Promise<unknown> {
  const res = await fetch(path)
  if (!res.ok) {
    throw new Error(`the ${what} request failed (${res.status}): ${(await res.text()).trim()}`)
  }
  const body: unknown = await res.json()
  return body
}

// With ?fixture=NAME in the address the page shows a scripted session and
// never contacts rehearse. It is how each state is looked at without an agent.
const fixtureName = new URLSearchParams(window.location.search).get('fixture')

function fixture(): FixtureSession | null {
  if (fixtureName === null) return null
  const session = fixtureSession(fixtureName)
  if (session === null) {
    throw new Error(`there is no fixture called "${fixtureName}". Use one of: ${fixtureNames.join(', ')}`)
  }
  return session
}

export async function getLedger(): Promise<Ledger> {
  return fixture()?.ledger() ?? parseLedger(await get('/api/ledger', 'ledger'))
}

export async function getHeld(): Promise<Held> {
  return fixture()?.held() ?? parseHeld(await get('/api/held', 'held actions'))
}

/** Performs the approved actions and refuses every other held action. */
export async function applyApproved(approve: string[]): Promise<void> {
  const scripted = fixture()
  if (scripted !== null) return scripted.apply(approve)
  await decide({ decision: 'apply', approve })
}

/** Refuses every held action. */
export async function discardAll(): Promise<void> {
  const scripted = fixture()
  if (scripted !== null) return scripted.apply([])
  await decide({ decision: 'discard' })
}

async function decide(body: { decision: 'apply'; approve: string[] } | { decision: 'discard' }): Promise<void> {
  const res = await fetch('/api/apply', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    throw new Error(`the decision was not accepted (${res.status}): ${(await res.text()).trim()}`)
  }
}

/**
 * onEvent gets every event, past and new. onConnection reports the
 * connection opening or dropping. onError gets an event that could not be
 * understood.
 */
export function subscribe(
  onEvent: (event: LogEvent) => void,
  onConnection: (connected: boolean) => void,
  onError: (error: Error) => void,
): Subscription {
  const scripted = fixtureName === null ? null : fixtureSession(fixtureName)
  if (scripted !== null) {
    onConnection(true)
    scripted.events().forEach(onEvent)
    return { close: () => undefined }
  }
  const source = new EventSource('/api/events')
  source.onopen = () => onConnection(true)
  source.onerror = () => onConnection(false)
  source.onmessage = (message: MessageEvent<unknown>) => {
    let event: LogEvent
    try {
      if (typeof message.data !== 'string') {
        throw new Error('event is not text')
      }
      const raw: unknown = JSON.parse(message.data)
      event = parseLogEvent(raw)
    } catch (e: unknown) {
      onError(e instanceof Error ? e : new Error(String(e)))
      return
    }
    onEvent(event)
  }
  return { close: () => source.close() }
}
