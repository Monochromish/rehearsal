// Where a session comes from: a running `rehearse` on this computer, or a
// scripted one for looking at the window on its own.

import type { Answer, Case, Changes, Entry, Info, Ledger, RecordCheck, SessionInfo } from './types'
import { fixture } from './fixtures'

export interface Source {
  info(): Promise<Info>
  sessions(): Promise<SessionInfo[]>
  // name is null for the live session
  ledger(name: string | null): Promise<Ledger>
  changes(name: string | null): Promise<Changes>
  text(name: string | null, id: string): Promise<string>
  entries(name: string): Promise<Entry[]>
  record(name: string): Promise<RecordCheck>
  apply(approve: string[]): Promise<void>
  // name is null for every recorded session together
  investigate(name: string | null, narrate: boolean): Promise<Case>
  ask(name: string | null, question: string): Promise<Answer>
}

async function get(url: string, what: string): Promise<Response> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${what}: the server answered ${res.status}`)
  return res
}

function live(base: string): Source {
  const at = (name: string | null, rest: string): string => (name === null ? `${base}/api/${rest}` : `${base}/api/sessions/${name}/${rest}`)
  return {
    info: async () => (await (await get(`${base}/api/info`, 'information')).json()) as Info,
    sessions: async () => (await (await get(`${base}/api/sessions`, 'sessions')).json()) as SessionInfo[],
    ledger: async (name) => (await (await get(at(name, 'ledger'), 'session')).json()) as Ledger,
    changes: async (name) => (await (await get(at(name, 'changes'), 'changes')).json()) as Changes,
    text: async (name, id) => await (await get(at(name, `change?id=${id}`), 'change')).text(),
    entries: async (name) => (await (await get(at(name, 'entries'), 'log')).json()) as Entry[],
    record: async (name) => (await (await get(at(name, 'record'), 'record')).json()) as RecordCheck,
    apply: async (approve) => {
      const res = await fetch(`${base}/api/apply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Origin: base },
        body: JSON.stringify(approve.length === 0 ? { decision: 'discard' } : { decision: 'apply', approve }),
      })
      if (!res.ok) throw new Error(`the decision was not accepted: ${res.status} ${await res.text()}`)
    },
    investigate: async (name, narrate) => {
      const query = [name === null ? '' : `session=${name}`, narrate ? 'narrate=1' : ''].filter((p) => p !== '').join('&')
      return (await (await get(`${base}/api/investigate${query === '' ? '' : `?${query}`}`, 'investigation')).json()) as Case
    },
    ask: async (name, question) => {
      const res = await fetch(`${base}/api/investigate/ask`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(name === null ? { question } : { session: name, question }),
      })
      if (!res.ok) throw new Error(`the question was not answered: ${res.status} ${await res.text()}`)
      return (await res.json()) as Answer
    },
  }
}

/** The source named by the app's settings: a fixture when one is named, else the live session. */
export function source(config: { url: string; fixture: string }): Source | null {
  if (config.fixture !== '') return fixture(config.fixture)
  return config.url === '' ? null : live(config.url)
}
