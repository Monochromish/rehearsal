// Words for the page. Everything here is worked out from facts the API
// already gives; none of it is written by the model.

import type { Held, HeldAction, Ledger, Risk } from './api'

/** One line on the decision screen: an action, or several identical ones. */
export interface Row {
  key: string
  actions: HeldAction[]
  // the first action: the others in the row are identical to it
  first: HeldAction
  risk: Risk
  title: string
  line: string
}

export function riskOf(a: HeldAction): Risk {
  return a.review?.risk ?? 'medium'
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

export function bytes(n: number): string {
  if (n < 1024) return plural(n, 'byte', 'bytes')
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

const nouns: Record<HeldAction['kind'], [string, string]> = {
  'git-push': ['push', 'pushes'],
  'npm-publish': ['publish', 'publishes'],
  gh: ['GitHub command', 'GitHub commands'],
  http: ['request', 'requests'],
  file: ['file change', 'file changes'],
}

const fileVerbs = { added: 'Add', changed: 'Change', deleted: 'Delete' }

/** What the action would do, in plain words. */
export function plainTitle(a: HeldAction): string {
  const to = a.destination
  switch (a.kind) {
    case 'git-push':
      if (a.force) return `Overwrite ${a.subject || 'what is there'} at ${to}`
      if (a.deletes) return `Delete ${a.subject.replace(' (delete)', '')} at ${to}`
      return `Push ${a.subject || 'changes'} to ${to}`
    case 'npm-publish':
      return `Publish ${a.subject || 'this package'} to ${to}`
    case 'gh':
      return `Run "gh ${a.subject}" on ${to.replace('GitHub repository ', '').replace('GitHub, for ', '')}`
    case 'http':
      return a.deletes ? `Ask ${to} to delete something` : `Send ${bytes(a.size)} to ${to}`
    case 'file': {
      const f = a.file
      if (f === null) return a.exact
      if (f.path === '.git' && f.dir) return "Update git's record of the project"
      const what = f.dir ? `the folder ${f.path} (${plural(f.files, 'file', 'files')})` : f.path
      return `${fileVerbs[f.change]} ${what}`
    }
  }
}

/** "what, from where -> where", for the monospace line. */
export function route(a: HeldAction, agent: string): string {
  if (a.file !== null) return `${a.file.change}, the sandbox -> ${a.workDir}/${a.file.path}`
  const what = a.kind === 'http' ? a.exact.replace(a.destination, '') : a.exact.split(' ').slice(0, a.kind === 'gh' ? 3 : 2).join(' ')
  return `${what}, ${a.workDir || agent} -> ${a.destination}`
}

/**
 * Turns actions into rows, in order of destination. Actions that are
 * identical in every way that shows (same command to the same place with
 * the same rating and outcome) share one row.
 */
export function rowsFor(actions: HeldAction[], agent: string): Row[] {
  const rows = new Map<string, Row>()
  for (const a of actions) {
    const key = [a.kind, a.destination, a.exact, riskOf(a), a.decision, a.outcome, a.review === null].join('\u0000')
    const row = rows.get(key)
    if (row === undefined) {
      rows.set(key, { key: a.id, actions: [a], first: a, risk: riskOf(a), title: plainTitle(a), line: route(a, agent) })
    } else {
      row.actions.push(a)
      row.title = `${plural(row.actions.length, ...nouns[a.kind])} to ${a.destination}`
    }
  }
  return [...rows.values()].sort((x, y) => x.first.destination.localeCompare(y.first.destination))
}

/**
 * The label of the button that carries out the decision. It states the
 * outcome and names anything that cannot be undone.
 */
export function decisionLabel(allowed: HeldAction[], refused: number): string {
  if (allowed.length === 0) return refused === 1 ? 'Refuse it' : `Refuse all ${refused}`
  const parts = [
    [allowed.filter((a) => a.kind === 'npm-publish').length, 'publish', 'publishes'],
    [allowed.filter((a) => a.force).length, 'force push', 'force pushes'],
    [allowed.filter((a) => a.deletes || a.file?.change === 'deleted').length, 'deletion', 'deletions'],
  ] as const
  const irreversible = parts.filter(([n]) => n > 0).map(([n, one, many]) => plural(n, one, many))
  let label = refused === 0 ? `Allow all ${allowed.length}` : `Allow ${allowed.length}, refuse ${refused}`
  if (allowed.length === 1 && refused === 0) label = 'Allow it'
  return irreversible.length === 0 ? label : `${label}, including ${irreversible.join(' and ')}`
}

export interface Outcome {
  word: string
  tone: 'high' | 'medium' | 'allowed' | 'plain'
}

/** What became of an action, once decided. Null while it is undecided. */
export function outcomeOf(a: HeldAction): Outcome | null {
  if (a.outcome === 'applied') return { word: 'Allowed', tone: 'allowed' }
  if (a.outcome === 'failed') return { word: 'Failed', tone: 'high' }
  if (a.outcome === 'not_applied') return { word: 'Not run', tone: 'medium' }
  if (a.decision === 'refused') return { word: 'Refused', tone: 'plain' }
  if (a.decision === 'approved') return { word: 'Waiting', tone: 'plain' }
  return null
}

export const riskWords: Record<Risk, string> = { high: 'High', medium: 'Medium', low: 'Low' }

/** What of the user's an action carries, for the report table. */
export function dataSent(a: HeldAction): string {
  if (a.file !== null) return 'Nothing: it stays on this computer'
  if (a.carries.length > 0) return a.carries.map((c) => c.label).join(', ')
  if (a.secretFiles.length > 0) return `Files that look like secrets: ${a.secretFiles.join(', ')}`
  if (a.files.length > 0) return plural(a.files.length + a.filesOmitted, 'file', 'files')
  if (a.size > 0) return bytes(a.size)
  return 'None of your details found'
}

function unusual(ledger: Ledger): number {
  return ledger.totals.flagged_servers + ledger.totals.held + ledger.sensitive.length + ledger.packages.filter((p) => p.blocked).length
}

/** The one sentence at the top of the page. */
export function statusSentence(ledger: Ledger, held: Held): string {
  const t = ledger.totals
  const traffic = `${plural(t.requests, 'request', 'requests')} to ${plural(t.servers, 'server', 'servers')}`
  const n = held.actions.length
  switch (ledger.phase) {
    case 'running':
      if (unusual(ledger) === 0) return `Watching. ${traffic}, all expected.`
      return `Watching. ${traffic}. ${plural(unusual(ledger), 'thing is', 'things are')} unusual.`
    case 'reviewing': {
      const done = held.actions.filter((a) => a.review !== null).length
      return `${plural(n, 'action is', 'actions are')} waiting. Reading them, ${done} of ${n} done.`
    }
    case 'deciding': {
      const high = held.actions.filter((a) => riskOf(a) === 'high').length
      const look = high === 0 ? 'None needs a careful look.' : `${high} ${high === 1 ? 'needs' : 'need'} a careful look.`
      return `${plural(n, 'action is', 'actions are')} waiting. ${look}`
    }
    case 'applying':
      return 'Doing what you allowed.'
    case 'finished': {
      if (n === 0) return 'Nothing needs you.'
      const count = (word: string): number => held.actions.filter((a) => outcomeOf(a)?.word === word).length
      const parts = [`${count('Allowed')} allowed`, `${count('Refused')} refused`]
      if (count('Failed') > 0) parts.push(`${count('Failed')} failed`)
      if (count('Not run') > 0) parts.push(`${count('Not run')} not run`)
      return `Done. ${parts.join(', ')}.`
    }
  }
}

/** The finished session as Markdown, for pasting into a note or a pull request. */
export function markdown(ledger: Ledger, held: Held): string {
  const cell = (s: string): string => s.replace(/\|/g, '\\|').replace(/\n/g, ' ')
  const lines = [`# Rehearsal: ${ledger.command[0] ?? 'agent'} session`, '', statusSentence(ledger, held), '']
  if (held.actions.length > 0) {
    lines.push('| Action | Destination | Data sent | Decision |', '|---|---|---|---|')
    for (const a of held.actions) {
      lines.push(`| \`${cell(a.exact)}\` | ${cell(a.destination)} | ${cell(dataSent(a))} | ${outcomeOf(a)?.word ?? 'Undecided'} |`)
    }
    lines.push('')
  }
  lines.push('| Server | Requests | Received |', '|---|---|---|')
  for (const s of ledger.servers) {
    const got = s.received.length === 0 ? 'None of your details' : s.received.map((r) => r.label).join(', ')
    lines.push(`| ${cell(s.host)} | ${s.requests} | ${cell(got)} |`)
  }
  return lines.join('\n') + '\n'
}
