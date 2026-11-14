// The page's only way to reach its data, so the transport (HTTP and
// server-sent events today) can be swapped without touching the components.
// Everything that arrives is checked here before the components see it.

export type Kind = 'real' | 'canary'
export type Category = 'provider' | 'other'
export type Where = 'address' | 'header' | 'body'
export type EventType = 'session_started' | 'request' | 'response' | 'identifier_seen' | 'session_ended'

const kinds: readonly Kind[] = ['real', 'canary']
const categories: readonly Category[] = ['provider', 'other']
const wheres: readonly Where[] = ['address', 'header', 'body']
const eventTypes: readonly EventType[] = ['session_started', 'request', 'response', 'identifier_seen', 'session_ended']

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
}

export interface Ledger {
  command: string[]
  ended: boolean
  totals: Totals
  watching: Identifier[]
  servers: Server[]
}

export interface LogEvent {
  seq: number
  type: EventType
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
  const endedAt = from['ended_at']
  if (endedAt !== null && typeof endedAt !== 'string') {
    throw new Error('ledger.ended_at is neither a time nor empty')
  }
  return {
    // null until the session_started event has been written
    command: from['command'] === null ? [] : list(from, 'command', what, (item, itemWhat) => text({ item }, 'item', itemWhat)),
    ended: endedAt !== null,
    totals: {
      requests: count(totals, 'requests', 'ledger.totals'),
      servers: count(totals, 'servers', 'ledger.totals'),
      identifiers_sent: count(totals, 'identifiers_sent', 'ledger.totals'),
      flagged_servers: count(totals, 'flagged_servers', 'ledger.totals'),
    },
    watching: list(from, 'watching', what, parseIdentifier),
    servers: list(from, 'servers', what, parseServer),
  }
}

function parseLogEvent(value: unknown): LogEvent {
  const from = fields(value, 'event')
  return { seq: count(from, 'seq', 'event'), type: oneOf(from, 'type', eventTypes, 'event') }
}

export async function getLedger(): Promise<Ledger> {
  const res = await fetch('/api/ledger')
  if (!res.ok) {
    throw new Error(`the ledger request failed (${res.status}): ${(await res.text()).trim()}`)
  }
  const body: unknown = await res.json()
  return parseLedger(body)
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
