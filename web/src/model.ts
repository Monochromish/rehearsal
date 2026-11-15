// Words and order for the window. Everything here is worked out from what
// the API gives; none of it is written by a model.

import type { Action, Changes, Claim, Identifier, Ledger, Part, Risk } from './types'

export type Group = 'out' | 'claim' | 'read' | 'skim' | 'mechanical'

/** One two-line row of the review list. */
export interface Item {
  id: string
  group: Group
  // what it is, and where or why
  title: string
  second: string
  // the title is a path or an address
  code: boolean
  tone: 'high' | 'medium' | 'plain'
  // the word or counts at the right edge
  edge: string
  edgeTone: 'high' | 'medium' | 'ok' | 'plain'
  action?: Action
  part?: Part
  claim?: Claim
  // the routine actions this one row stands for, and whether they are listed under it
  bundle?: Action[]
  unfolded?: boolean
}

export const groupNames: Record<Group, string> = {
  out: 'Leaving this computer',
  claim: 'Claimed, but not what happened',
  read: 'To read',
  skim: 'To skim',
  mechanical: 'Mechanical',
}

/** What the decision box says under any button. */
export const consequence = 'Unticked actions are refused.'
/** The id of the one row that stands for every routine action. */
export const routineRow = 'routine'


const riskWords: Record<Risk, string> = { high: 'High', medium: 'Medium', low: 'Low' }

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

/** Writes paths under the home folder with a ~. */
export function tilde(text: string, home: string | undefined): string {
  if (home === undefined || home === '' || home === '/') return text
  return text.split(home + '/').join('~/')
}

function called(item: Identifier): string {
  return item.name !== undefined && item.name !== '' ? item.name : item.label.charAt(0).toLowerCase() + item.label.slice(1)
}

function carried(a: Action): string {
  const names = a.carries.map(called)
  return names.length <= 2 ? names.join(' and ') : `${names.slice(0, 2).join(', ')} and ${names.length - 2} more`
}

function where(a: Action, home: string | undefined): string {
  const to = tilde(a.destination, home)
  return a.remote !== undefined && a.remote !== '' ? `${a.remote} (${to})` : to
}

/** The headline: what the action would do, said specifically. */
export function headline(a: Action, home: string | undefined): string {
  const to = where(a, home)
  switch (a.kind) {
    case 'git-push':
      if (a.force === true) return `Overwrite ${a.subject ?? 'what is there'} at ${to}`
      if (a.deletes === true) return `Delete ${(a.subject ?? '').replace(' (delete)', '')} at ${to}`
      return `Push ${a.subject ?? 'changes'} to ${to}`
    case 'npm-publish':
      return `Publish ${a.subject ?? 'this package'} to ${to}`
    case 'gh':
      // the command up to its first option, which says which pull request or issue
      return `Run "${(a.command ?? ['gh', a.subject ?? '']).filter((w, i) => i < 4 && !w.startsWith('-')).join(' ')}" on ${to.replace('GitHub repository ', '')}`
    case 'http':
      if (a.deletes === true) return `Ask ${to} to delete ${a.path ?? 'something'}`
      if (a.carries.length > 0) return `Send ${carried(a)} to ${to}`
      return (a.request_bytes ?? 0) === 0 ? `Send a request to ${to}` : `Send ${a.request_bytes} bytes to ${to}`
    case 'file': {
      const f = a.file
      if (f === undefined) return 'Change a file'
      return `${{ added: 'Add', changed: 'Change', deleted: 'Delete' }[f.change]} ${f.path}`
    }
  }
}

/** A path's last two parts as a thing with a name: "/v1/databases/production" is "database production". */
function thing(path: string | undefined): string {
  const segs = (path ?? '').split('/').filter((s) => s !== '')
  const name = segs[segs.length - 1]
  const kind = segs[segs.length - 2]
  if (name === undefined) return 'something'
  if (kind === undefined || !/^[a-z]+s$/.test(kind)) return path ?? name
  return `${kind.slice(0, -1)} ${name}`
}

function branch(a: Action): string {
  return (a.subject ?? 'a branch').replace(/^branch /, '').replace(' (delete)', '')
}

/** The first line of a row: what it would do and where, in as few words as say it. */
function rowTitle(a: Action, home: string | undefined): string {
  if (a.kind === 'http' && a.deletes === true) return `Delete ${thing(a.path)} on ${a.host ?? a.destination}`
  if (a.kind === 'git-push' && a.force === true) return `Force-push ${branch(a)} to ${a.remote ?? tilde(a.destination, home)}`
  if (a.kind === 'git-push') return `Push ${branch(a)} to ${a.remote ?? tilde(a.destination, home)}`
  return headline(a, home)
}

/**
 * The second line of a row: the most surprising fact about the action,
 * from its own fields. It is not a description of the request and no
 * model wrote it.
 */
export function surprise(a: Action, home: string | undefined): string {
  if (a.provenance !== undefined) return `Address from ${a.provenance.source_host}`
  if (a.provider_door === true) return 'A different login'
  if ((a.secret_files ?? []).length > 0) return `Includes ${(a.secret_files ?? [])[0] ?? 'a secret file'}`
  if (a.force === true) return 'Overwrites the remote'
  if (a.deletes === true) return 'Removes it there'
  if (a.kind === 'npm-publish') return 'Makes it public'
  if ((a.outside_sources ?? []).length > 0) return `First contact, after reading ${(a.outside_sources ?? [])[0] ?? 'outside content'}`
  // the title already says what it carries: say where in the request it is
  if (a.carries.length > 0 && a.kind === 'http') {
    const query = a.query !== undefined && a.query.includes('[') ? `?${a.query}` : ''
    return query !== '' ? `In the address: ${a.path ?? ''}${query}` : `${a.method ?? ''} ${a.path ?? ''}`.trim()
  }
  if (a.carries.length > 0) return `Carries ${carried(a)}`
  if (a.kind === 'git-push') return plural((a.commits ?? []).length, 'commit', 'commits')
  return where(a, home)
}

/**
 * An action named two ways: as what the agent tried to do ("delete
 * database production") and as a thing among others ("a force push to main").
 */
export function serious(a: Action, home: string | undefined): { verb: string; noun: string; tried: string } {
  const to = a.host ?? tilde(a.destination, home)
  if (a.kind === 'http' && a.deletes === true) {
    const what = thing(a.path)
    return { verb: `delete ${what}`, noun: `deleting ${what}`, tried: `delete ${what} on ${to}` }
  }
  if (a.kind === 'git-push' && a.force === true) {
    return { verb: `force-push ${branch(a)}`, noun: `a force push to ${branch(a)}`, tried: `overwrite ${branch(a)} on ${a.remote ?? to}` }
  }
  if (a.carries.length > 0) {
    const send = `send ${carried(a)} to ${to}`
    const tried = a.provenance !== undefined ? `send ${carried(a)} to a server named by ${a.provenance.source_host}` : send
    return { verb: send, noun: `sending ${carried(a)} to ${to}`, tried }
  }
  if (a.kind === 'npm-publish') {
    const what = a.subject ?? 'a package'
    return { verb: `publish ${what}`, noun: `publishing ${what}`, tried: `publish ${what}` }
  }
  const title = rowTitle(a, home)
  const lower = title.charAt(0).toLowerCase() + title.slice(1)
  return { verb: lower, noun: lower, tried: lower }
}

const rank: Record<Risk, number> = { high: 0, medium: 1, low: 2 }

/** The worst of the actions: high risk, and among those the one that sends something out first. */
export function worst(actions: Action[]): Action | null {
  const high = actions.filter((a) => a.review?.risk === 'high')
  const weight = (a: Action): number => (a.carries.length > 0 ? 0 : a.deletes === true ? 1 : a.force === true ? 2 : 3)
  return [...high].sort((x, y) => weight(x) - weight(y))[0] ?? null
}

function routine(a: Action): boolean {
  return a.review?.risk === 'low'
}

/** The request or command exactly as issued, with values masked. */
export function evidence(a: Action, home: string | undefined): string {
  if (a.kind === 'http') {
    const scheme = a.scheme !== undefined && a.scheme !== '' ? `${a.scheme}://` : ''
    const query = a.query !== undefined && a.query !== '' ? `?${a.query}` : ''
    return `${a.method ?? ''} ${scheme}${a.host ?? ''}${a.path ?? ''}${query}`
  }
  const lines = [tilde((a.command ?? []).join(' '), home)]
  if ((a.commits ?? []).length > 0) lines.push('', ...(a.commits ?? []))
  if ((a.files ?? []).length > 0) lines.push('', ...(a.files ?? []))
  return lines.join('\n')
}

export function outcome(a: Action): { word: string; tone: 'high' | 'medium' | 'ok' | 'plain' } | null {
  if (a.outcome === 'applied') return { word: 'Allowed', tone: 'ok' }
  if (a.outcome === 'failed') return { word: 'Failed', tone: 'plain' }
  if (a.outcome === 'not_applied') return { word: 'Not run', tone: 'plain' }
  if (a.decision === 'refused') return { word: 'Refused', tone: 'plain' }
  return null
}

function counts(p: Part): string {
  if (p.status === 'renamed' && p.added + p.removed === 0) return 'moved'
  const parts: string[] = []
  if (p.added > 0) parts.push(`+${p.added}`)
  if (p.removed > 0) parts.push(`-${p.removed}`)
  return parts.join(' ')
}

function sentence(reason: string): string {
  return reason.charAt(0).toUpperCase() + reason.slice(1)
}

/**
 * The review list in order: what wants to leave, claims the commands
 * contradict, then the changes by how much reading they need.
 */
export function items(ledger: Ledger, changes: Changes, performing: boolean, unfolded = false): Item[] {
  const out: Item[] = []
  const row = (a: Action): Item => {
    const done = outcome(a)
    const risk = a.review?.risk ?? 'medium'
    let edge = a.review === null ? 'Reading…' : riskWords[risk]
    let edgeTone: Item['edgeTone'] = a.review === null || risk === 'low' ? 'plain' : risk
    if (done !== null) {
      edge = done.word
      edgeTone = done.tone
    } else if (performing && a.decision === 'approved') {
      edge = 'Performing…'
      edgeTone = 'plain'
    }
    return { id: a.id, group: 'out', title: rowTitle(a, ledger.home), second: surprise(a, ledger.home), code: false, tone: 'plain', edge, edgeTone, action: a }
  }
  // a file change is shown with the changes; the rest most serious first, in the order they came
  const held = ledger.actions.filter((a) => a.kind !== 'file')
  const ordered = [...held].sort((x, y) => rank[x.review?.risk ?? 'medium'] - rank[y.review?.risk ?? 'medium'])
  const low = ordered.filter(routine)
  for (const a of ordered) {
    if (low.length < 2 || !routine(a)) out.push(row(a))
  }
  if (low.length >= 2) {
    // routine actions are one row with one tick; they are listed under it when it is opened
    const places = [...new Set(low.map((a) => where(a, ledger.home)))]
    const words = [...new Set(low.map((a) => row(a).edge))]
    out.push({
      id: routineRow,
      group: 'out',
      title: `${low.length} routine actions`,
      second: places.length === 1 ? `All to ${places[0] ?? ''}` : `To ${places.length} places`,
      code: false,
      tone: 'plain',
      edge: words.length === 1 ? (words[0] ?? 'Low') : 'Low',
      edgeTone: 'plain',
      bundle: low,
      unfolded,
    })
    if (unfolded) for (const a of low) out.push(row(a))
  }
  for (const [i, c] of (ledger.report.notes?.claims ?? []).entries()) {
    if (c.verdict !== 'contradicted') continue
    out.push({
      id: `claim-${i}`,
      group: 'claim',
      title: `"${c.claim}"`,
      second: c.command !== undefined && c.command !== '' ? c.command : 'No command supports it',
      code: false,
      tone: 'plain',
      edge: '',
      edgeTone: 'plain',
      claim: c,
    })
  }
  for (const p of changes.parts) {
    out.push({
      id: p.id,
      group: p.level,
      title: p.line !== undefined && p.line > 0 ? `${p.path}:${p.line}` : p.path,
      second: sentence(p.reason),
      code: true,
      tone: 'plain',
      edge: counts(p),
      edgeTone: 'plain',
      part: p,
    })
  }
  return out
}

/** The sentence at the top: the actual workload. */
export function workload(ledger: Ledger, _changes: Changes, list: Item[]): string {
  const held = ledger.actions.filter((a) => a.kind !== 'file').map((a) => ({ action: a, edge: outcome(a)?.word ?? '' }))
  const open = held.filter((i) => outcome(i.action) === null).map((i) => i.action)
  const waiting = open.length
  const read = list.filter((i) => i.group === 'read').length
  const stopped = ledger.packages.filter((p) => p.blocked).length
  switch (ledger.phase) {
    case 'running': {
      const t = ledger.totals
      const unusual = t.flagged_servers + t.held + ledger.sensitive.length + stopped
      const traffic = `${plural(t.requests, 'request', 'requests')} to ${plural(t.servers, 'server', 'servers')} so far`
      return unusual === 0 ? `The agent is working. ${traffic}, all expected.` : `The agent is working. ${traffic}, and ${plural(unusual, 'thing is', 'things are')} unusual.`
    }
    case 'reviewing': {
      const done = held.filter((i) => i.action.review !== null).length
      return `${plural(held.length, 'action is', 'actions are')} waiting for you. Reading them, ${done} of ${held.length} done.`
    }
    case 'applying':
      return 'Doing what you allowed.'
    case 'deciding':
    case 'finished': {
      // the single most serious thing in plain words, then a count of the rest
      const top = worst(open)
      if (top !== null) {
        const rest = open.length - 1
        const others = open.filter((a) => a !== top)
        const said = [`The agent tried to ${serious(top, ledger.home).tried}.`]
        if (rest > 0) said.push(others.every(routine) ? `${plural(rest, 'routine action is', 'routine actions are')} also waiting.` : `${plural(rest, 'other is', 'others are')} waiting.`)
        if (read > 0) said.push(`${plural(read, 'change is', 'changes are')} worth reading.`)
        return said.join(' ')
      }
      const parts: string[] = []
      if (waiting > 0) parts.push(open.every(routine) ? `${plural(waiting, 'routine action is', 'routine actions are')} waiting` : `${plural(waiting, 'action is', 'actions are')} waiting, none high risk`)
      if (read > 0) parts.push(`${plural(read, 'change is', 'changes are')} worth reading`)
      if (parts.length === 0) {
        if (ledger.phase === 'finished' && held.length > 0) {
          const n = (word: string): number => held.filter((i) => i.edge === word).length
          const said = [`${n('Allowed')} allowed`, `${n('Refused')} refused`]
          if (n('Failed') > 0) said.push(`${n('Failed')} failed`)
          if (stopped > 0) said.push(plural(stopped, 'install stopped', 'installs stopped'))
          return `Done. ${said.join(', ')}.`
        }
        return 'Nothing needs you.'
      }
      return `${parts.join(', and ')}.`
    }
  }
}

/**
 * The label of the button that carries out the decision. It names the most
 * serious thing being allowed, so that it is read before it is pressed.
 */
export function decisionLabel(held: Action[], ticked: Set<string>, home?: string): string {
  const open = held.filter((a) => outcome(a) === null)
  const yes = open.filter((a) => ticked.has(a.id))
  const no = open.length - yes.length
  if (yes.length === 0) return no === 1 ? 'Refuse it' : `Refuse all ${no}`
  const top = worst(yes)
  if (top === null) return yes.every(routine) ? `Allow ${plural(yes.length, 'routine action', 'routine actions')}` : `Allow ${plural(yes.length, 'action', 'actions')}`
  if (yes.length === 1) return `Allow: ${serious(top, home).verb}`
  return `Allow ${yes.length}, including ${serious(top, home).noun}`
}

/**
 * Every piece of text the review list shows before anything is clicked,
 * in the order it is shown: the sentence, each group's name, each row,
 * each folded group's line, and the decision. The window draws these same
 * strings; a test counts their words.
 */
export function visibleText(ledger: Ledger, changes: Changes, ticked: Set<string>): string[] {
  const list = items(ledger, changes, false)
  const out = [workload(ledger, changes, list)]
  const order: Group[] = ['out', 'claim', 'read', 'skim', 'mechanical']
  for (const g of order) {
    const rows = list.filter((i) => i.group === g)
    if (rows.length === 0) continue
    out.push(groupNames[g])
    if (g === 'skim' || g === 'mechanical') {
      out.push('Show')
      continue
    }
    for (const r of rows) out.push(r.title, r.second, r.bundle !== undefined ? 'Show' : r.edge)
  }
  const held = ledger.actions.filter((a) => a.kind !== 'file')
  if (held.some((a) => outcome(a) === null)) out.push(consequence, decisionLabel(held, ticked, ledger.home))
  return out
}

export function words(text: string[]): number {
  return text.join(' ').split(/\s+/).filter((w) => w !== '').length
}

/** The facts of a finished session, one per line; empty where a figure was not read. */
export function facts(ledger: Ledger): string[] {
  const r = ledger.report
  const out: string[] = []
  const t = ledger.totals
  out.push(`${plural(t.requests, 'request', 'requests')} to ${plural(t.servers, 'server', 'servers')}.`)
  const first = r.models[0]
  if (first !== undefined) {
    const rest = r.models.slice(1).map((m) => `${m.name} (${m.requests})`)
    out.push(`Model: ${first.name}, ${plural(first.requests, 'request', 'requests')}.${rest.length > 0 ? ` Also used: ${rest.join(', ')}.` : ''}`)
  }
  const tok = r.tokens
  const known: string[] = []
  const add = (n: number | null, what: string): void => {
    if (n !== null) known.push(`${n.toLocaleString()} ${what}`)
  }
  add(tok.input, 'in')
  add(tok.output, 'out')
  add(tok.cache_read, 'read from cache')
  add(tok.cache_write, 'written to cache')
  if (known.length > 0) out.push(`Tokens: ${known.join(', ')}.`)
  if (r.commands_known) out.push(`The agent ran ${plural(r.commands.length, 'shell command', 'shell commands')}.`)
  if (ledger.outside.length > 0) out.push(`Content read from other servers: ${ledger.outside.map((o) => (o.hidden > 0 ? `${o.host} (${o.hidden} hidden)` : o.host)).join(', ')}.`)
  if (ledger.changes !== null) {
    const c = ledger.changes
    if (!c.in_repo) out.push('The working folder is not in a git repository, so its changes were not recorded.')
    else if (c.problem !== undefined && c.problem !== '') out.push(`The changes could not be recorded: ${c.problem}`)
    else out.push(c.files === 0 ? 'No file in the project changed.' : `${plural(c.files, 'file', 'files')} changed: ${c.added} lines added, ${c.removed} removed.`)
  }
  for (const f of r.flags) out.push(`Worth a second look: ${f}`)
  return out
}

/** What is unusual so far, for while the agent is running. */
export function unusual(ledger: Ledger): { title: string; second: string }[] {
  const out: { title: string; second: string }[] = []
  for (const s of ledger.servers.filter((x) => x.flagged)) {
    out.push({ title: s.host, second: `Received ${s.received.map((r) => r.label).join(', ')}` })
  }
  for (const w of ledger.sensitive) out.push({ title: w.path, second: w.reason })
  for (const p of ledger.packages.filter((x) => x.blocked)) out.push({ title: `An ${p.manager} install was stopped`, second: p.facts })
  for (const a of ledger.actions) out.push({ title: headline(a, ledger.home), second: 'Held until you decide' })
  return out
}
