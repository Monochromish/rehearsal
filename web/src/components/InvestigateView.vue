<script setup lang="ts">
// What the rules found in one recorded session or in all of them. Short
// to read: a headline and one line for each finding, one line of custody.
// Nothing is taken away to get there: the chain, the entry that proves
// the finding and every statement are in the detail, and every reference
// opens the raw line it names.
import Glyph from './Glyph.vue'
import SegmentedControl from './SegmentedControl.vue'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import type { Source } from '../api'
import type { Answer, Case, CaseEntry, EntryRef, Finding } from '../types'
import RawEntry from './RawEntry.vue'

const props = defineProps<{ source: Source; session: string; narrow: boolean; select: string; reviewer: string; all: boolean }>()
// what the window's status line says while this view is up: a fact about the record, and whether it is a failure
const emit = defineEmits<{ status: [text: string, failing: boolean] }>()

const scope = ref<'one' | 'all'>(props.session === '' ? 'all' : 'one')
const data = ref<Case | null>(null)
const problem = ref('')
const picked = ref('')
const opened = ref<CaseEntry | null>(null)
const openedMark = ref('')
const cell = ref<{ label: string; kind: string } | null>(null)
const question = ref('')
const answer = ref<Answer | null>(null)
const asking = ref(false)
const writing = ref(false)
// what is unfolded: 'custody', 'limits', and in a finding 'entries', 'links', 'how'
const shown = ref<string[]>([])

// one word each, for the overview's columns
const column: Record<string, string> = {
  'sent-after-reading': 'Chain',
  'encoded-value': 'Encoded',
  'destructive-request': 'Delete',
  'high-risk-action': 'Risk',
  'value-sent': 'Sent',
  'new-server-after-reading': 'New',
  'contradicted-claim': 'Claim',
  'stopped-install': 'Install',
  'same-destination': 'Repeat',
}

function target(): string | null {
  return scope.value === 'one' && props.session !== '' ? props.session : null
}

function fold(name: string): void {
  shown.value = shown.value.includes(name) ? shown.value.filter((n) => n !== name) : [...shown.value, name]
}

async function load(narrate: boolean): Promise<void> {
  try {
    data.value = await props.source.investigate(target(), narrate)
    problem.value = ''
    if (picked.value === '' && props.select !== '') picked.value = props.select
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

onMounted(async () => {
  // The native control shows its first choice until its value changes, so
  // a start on "every session" is made as a change once it is on screen.
  if (props.all && scope.value !== 'all') {
    await nextTick()
    scope.value = 'all'
    return
  }
  await load(false)
})
watch([scope, () => props.session], () => {
  picked.value = ''
  opened.value = null
  cell.value = null
  answer.value = null
  void load(false)
})

const findings = computed<Finding[]>(() => {
  const all = data.value?.findings ?? []
  const at = cell.value
  return at === null ? all : all.filter((f) => f.kind === at.kind && f.sessions.includes(at.label))
})
const selected = computed<Finding | null>(() => data.value?.findings.find((f) => f.id === picked.value) ?? null)
const heading = computed(() => {
  const c = data.value
  if (c === null) return problem.value === '' ? 'Reading the record…' : 'The record could not be read'
  const n = c.findings.length
  const from = c.records.length === 1 ? 'this session' : `${c.records.length} sessions`
  return n === 0 ? `Nothing to report in ${from}` : `${n} ${n === 1 ? 'finding' : 'findings'} in ${from}`
})
/** Whether a record's signature was checked and did not hold, which is a failure, or was only not checked. */
function signatureFails(signature: string): boolean {
  return signature.startsWith('does not hold')
}

/**
 * Custody in one line. A failed check is said whole, as a danger. When
 * every entry holds, the line is in three parts so that the one word
 * "intact" can carry the colour for allowed.
 */
const custody = computed(() => {
  const list = data.value?.records ?? []
  const total = list.reduce((n, r) => n + r.entries, 0)
  const bad = list.reduce((n, r) => n + r.bad_entries.length, 0)
  if (bad > 0) {
    const where =
      list.length > 1
        ? `, in ${list
            .filter((r) => r.bad_entries.length > 0)
            .map((r) => r.label)
            .join(' and ')}`
        : ''
    return { failing: true, sigBad: false, text: `${bad} of ${total} entries ${bad === 1 ? 'fails its' : 'fail their'} check${where}`, before: '', after: '' }
  }
  const before = `All ${total} entries`
  const unsigned = list.filter((r) => !r.verified)
  const sigBad = unsigned.some((r) => signatureFails(r.signature))
  let after = list.length === 1 ? 'and the signature is valid' : `and ${list.length} signatures are valid`
  if (unsigned.length > 0) {
    if (list.length > 1) after = `and ${unsigned.length} of ${list.length} signatures ${sigBad ? 'do not hold' : 'are not checked'}`
    else after = sigBad ? 'and the signature does not hold' : 'and the signature is not checked yet'
  }
  return { failing: false, sigBad, text: `${before} intact ${after}`, before, after }
})

function stateOf(r: { verified: boolean; bad_entries: number[]; signature: string }): { word: string; tone: string } {
  if (r.verified) return { word: 'Intact', tone: 'state ok' }
  if (r.bad_entries.length > 0 || signatureFails(r.signature)) return { word: 'Not verified', tone: 'state bad' }
  return { word: 'Signature not checked', tone: 'state' }
}

/** The one warning a finding carries: the entry that fails, or failing that, the state of its record. */
const warning = computed<{ text: string; failed: boolean } | null>(() => {
  const f = selected.value
  const c = data.value
  if (f === null || c === null) return null
  if (f.unverified.length > 0) {
    const names = f.unverified.map(cite).join(' and ')
    return { text: `Rests on ${names}, which ${f.unverified.length === 1 ? 'fails its' : 'fail their'} check. The record cannot be trusted there.`, failed: true }
  }
  const records = c.records.filter((r) => f.sessions.includes(r.label) && !r.verified)
  if (records.length === 0) return null
  const failed = records.some((r) => r.bad_entries.length > 0 || signatureFails(r.signature))
  const labels = records.map((r) => r.label).join(' and ')
  return failed ? { text: `From a record that does not verify (${labels}). The entries this rests on match their hashes.`, failed } : { text: `From a record whose signature is not checked yet (${labels}).`, failed }
})

function cite(r: EntryRef): string {
  return `${r.label}#${r.seq}`
}
function entryOf(r: EntryRef): CaseEntry | undefined {
  return data.value?.entries.find((e) => e.label === r.label && e.seq === r.seq)
}
/** The raw line cut at the part that supports the statement. */
function parts(text: string, mark: string): [string, string, string] {
  const i = mark === '' ? -1 : text.indexOf(mark)
  return i < 0 ? [text, '', ''] : [text.slice(0, i), mark, text.slice(i + mark.length)]
}
function open(r: EntryRef | undefined): void {
  if (r === undefined) return
  const e = entryOf(r)
  if (e === undefined) {
    void openItem(r.label, r.session, r.seq)
    return
  }
  opened.value = e
  openedMark.value = r.mark ?? ''
}
/** Opens any entry of a timeline, cited or not, through the session's own log. */
async function openItem(label: string, session: string, seq: number): Promise<void> {
  const known = data.value?.entries.find((e) => e.label === label && e.seq === seq)
  if (known !== undefined) {
    opened.value = known
    openedMark.value = selected.value?.refs.find((r) => r.label === label && r.seq === seq)?.mark ?? ''
    return
  }
  try {
    const found = (await props.source.entries(session)).find((e) => e.seq === seq)
    if (found === undefined) return
    opened.value = { session, label, seq, time: found.time, type: found.type, data: JSON.stringify(found.data), prev: found.prev, hash: found.hash }
    openedMark.value = ''
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  }
}

/** The entries a finding rests on other than the one shown in full. */
const rest = computed(() => {
  const f = selected.value
  return f === null ? [] : f.refs.filter((r) => !(r.label === f.key.label && r.seq === f.key.seq))
})

const maxTicks = 240
/** The stretch of each session's timeline around the finding's entries. */
const timelines = computed(() => {
  const c = data.value
  const f = selected.value
  if (c === null || f === null) return []
  return c.timeline
    .filter((t) => f.sessions.includes(t.label))
    .map((t) => {
      const mine = f.refs.filter((r) => r.label === t.label).map((r) => r.seq)
      let from = Math.max(1, Math.min(...mine) - 12)
      const to = Math.min(t.items.length, Math.max(Math.max(...mine) + 12, from + 40))
      if (to - from > maxTicks) from = to - maxTicks
      const items = t.items.filter((i) => i.seq >= from && i.seq <= to)
      return { ...t, items, from, to, total: t.items.length }
    })
})

async function ask(): Promise<void> {
  const q = question.value.trim()
  if (q === '' || asking.value) return
  asking.value = true
  try {
    answer.value = await props.source.ask(target(), q)
    problem.value = ''
  } catch (e: unknown) {
    problem.value = e instanceof Error ? e.message : String(e)
  } finally {
    asking.value = false
  }
}
async function write(): Promise<void> {
  writing.value = true
  await load(true)
  writing.value = false
}
watch(custody, (c) => emit('status', data.value === null ? '' : c.text, c.failing || c.sigBad), { immediate: true })

function pickCell(label: string, kind: string, ids: string[]): void {
  if (ids.length === 0) return
  const same = cell.value !== null && cell.value.label === label && cell.value.kind === kind
  cell.value = same ? null : { label, kind }
}
function pick(id: string): void {
  picked.value = id
  opened.value = null
  shown.value = shown.value.filter((n) => n === 'custody' || n === 'limits')
}
</script>

<template>
  <div :class="narrow ? 'inv column' : 'inv'">
    <div v-if="!(narrow && selected !== null)" :class="selected !== null ? 'list open' : 'list'">
      <div class="scroll">
        <div class="col">
          <h1 class="status">{{ heading }}</h1>
          <span v-if="problem !== ''" class="problem">{{ problem }}</span>
          <SegmentedControl v-if="session !== ''" v-model="scope" class="scope">
            <option value="one">This session</option>
            <option value="all">Every session</option>
          </SegmentedControl>

          <template v-if="data !== null">
            <span class="label">CUSTODY</span>
            <div class="line" @click="fold('custody')">
              <span v-if="custody.failing" class="grow bad">{{ custody.text }}</span>
              <div v-else class="grow words">
                <span>{{ custody.before }}</span>
                <span class="ok">intact</span>
                <span :class="custody.sigBad ? 'bad' : ''">{{ custody.after }}</span>
              </div>
              <span class="show">{{ shown.includes('custody') ? 'Hide' : 'Show' }}</span>
            </div>
            <template v-if="shown.includes('custody')">
              <div v-for="r in data.records" :key="r.label" class="record">
                <div class="line">
                  <span class="mono strong">{{ r.label }}</span>
                  <span class="mono grow">{{ r.session }}</span>
                  <span :class="stateOf(r).tone">{{ stateOf(r).word }}</span>
                </div>
                <span class="fact">Chain: {{ r.chain }}.</span>
                <span class="fact">Signature: {{ r.signature }}.</span>
                <span class="fact">To check it yourself:</span>
                <span class="mono cmd">{{ r.command }}</span>
              </div>
            </template>

            <template v-if="data.records.length > 1">
              <span class="label">SESSIONS</span>
              <div class="matrix">
                <div class="mrow">
                  <span class="mhead first"></span>
                  <span v-for="k in data.kinds" :key="k.kind" class="mhead" :title="k.title">{{ column[k.kind] ?? k.kind }}</span>
                </div>
                <div v-for="row in data.overview" :key="row.label" class="mrow">
                  <span class="mhead first mono" :title="row.session">{{ row.label }}</span>
                  <div
                    v-for="(ids, i) in row.cells"
                    :key="i"
                    :class="[ids.length > 0 ? 'mcell has' : 'mcell', cell !== null && cell.label === row.label && cell.kind === data.kinds[i]?.kind ? 'on' : '']"
                    @click="pickCell(row.label, data.kinds[i]?.kind ?? '', ids)"
                  >
                    <span class="mono">{{ ids.length > 0 ? ids.length : '' }}</span>
                  </div>
                </div>
              </div>
              <span class="fact dim">{{
                cell === null ? 'A filled cell is a session with findings of that kind. Select one to list them.' : `Listing ${column[cell.kind] ?? cell.kind} in ${cell.label}. Select the cell again to list everything.`
              }}</span>
            </template>

            <span class="label">{{ findings.length === 0 ? 'NO FINDINGS' : 'FINDINGS' }}</span>
            <div v-for="f in findings" :key="f.id" :class="f.id === picked ? 'row current' : 'row'" @click="pick(f.id)">
              <div class="rowtext">
                <span class="rowtitle">{{ f.headline }}</span>
                <span class="rowmeta">{{ f.support }}</span>
                <span v-if="f.unverified.length > 0" class="rowmeta bad">Rests on an entry that fails its check</span>
              </div>
              <span class="mono edge">{{ f.sessions.join(' ') }}</span>
            </div>

            <span class="label">ASK</span>
            <div class="askrow">
              <input v-model="question" class="grow" type="text" placeholder="Was anything sent to a server the user did not name?" @keydown.enter="ask" />
              <button :disabled="asking || question.trim() === ''" @click="ask">{{ asking ? 'Reading…' : 'Ask' }}</button>
            </div>
            <div v-if="answer !== null" class="plain">
              <span v-if="answer.text" class="fact">{{ answer.text }}</span>
              <div v-for="(s, i) in answer.sentences" :key="i" class="statement">
                <span class="kind">Model</span>
                <span class="fact">{{ s.text }}</span>
                <div class="cites">
                  <div v-for="r in s.refs" :key="cite(r)" class="chip" @click="openItem(r.label, r.session, r.seq)">
                    <span class="chiptext">{{ cite(r) }}</span>
                  </div>
                </div>
              </div>
              <span v-if="answer.removed > 0" class="fact dim">{{ answer.removed }} removed for citing nothing the model was given.</span>
              <span v-if="answer.given.length > 0" class="fact dim">Entries the answer was written from:</span>
              <div class="cites">
                <div v-for="r in answer.given" :key="cite(r)" class="chip" @click="openItem(r.label, r.session, r.seq)">
                  <span class="chiptext">{{ cite(r) }}</span>
                </div>
              </div>
            </div>

            <div class="line foldline" @click="fold('limits')">
              <span class="grow dim">What the record cannot show</span>
              <span class="show">{{ shown.includes('limits') ? 'Hide' : 'Show' }}</span>
            </div>
            <template v-if="shown.includes('limits')">
              <span v-for="(l, i) in data.limits" :key="i" class="fact limit">{{ l }}</span>
              <span class="fact dim">Rehearsal {{ data.tool_version }}, rule set {{ data.rule_set }}. The same records give the same findings every time.</span>
            </template>
          </template>
        </div>
      </div>
      <div v-if="opened !== null && selected === null" class="entrybar">
        <div class="top">
          <div class="close" title="Close the entry" @click="opened = null">
            <Glyph lucide="x" :size="16" :stroke-width="1.5" />
          </div>
        </div>
        <RawEntry
          :cite="`${opened.label}#${opened.seq}`"
          :type="opened.type"
          :time="opened.time"
          :data="opened.data"
          :hash="opened.hash"
          :prev="opened.prev"
          :mark="openedMark"
          :problem="opened.problem"
        />
      </div>
    </div>

    <div v-if="selected !== null && !narrow" class="divider"></div>
    <div v-if="selected !== null && data !== null" class="detail">
      <div class="scroll">
        <div class="col wide">
          <div :class="narrow ? 'top alone' : 'top'">
            <div v-if="narrow" class="back" @click="pick('')">
              <Glyph lucide="arrow-left" :size="16" :stroke-width="1.5" />
              <span>Back to the list</span>
            </div>
            <div v-else class="close" title="Close" @click="pick('')">
              <Glyph lucide="x" :size="16" :stroke-width="1.5" />
            </div>
          </div>
          <h1 class="title">{{ selected.headline }}</h1>
          <span class="rowmeta">{{ selected.support }}</span>
          <span v-if="warning !== null" :class="warning.failed ? 'note bad' : 'note'">{{ warning.text }}</span>

          <span class="label">HOW IT HAPPENED</span>
          <div v-for="(s, i) in selected.chain" :key="i" class="stepline">
            <span class="num">{{ i + 1 }}</span>
            <span class="grow steptext">{{ s.label }}</span>
            <span v-if="s.rule" class="mono rulename">rule {{ s.rule }}</span>
            <div v-else class="cites tight">
              <div v-for="r in s.refs" :key="cite(r)" class="chip" @click="open(r)">
                <span class="chiptext">{{ cite(r) }}</span>
              </div>
            </div>
          </div>

          <span class="label">EVIDENCE</span>
          <RawEntry
            :cite="cite(selected.key)"
            :type="entryOf(selected.key)?.type ?? ''"
            :time="entryOf(selected.key)?.time ?? ''"
            :data="entryOf(selected.key)?.data ?? ''"
            :hash="selected.key.hash"
            :mark="selected.key.mark ?? ''"
            :problem="entryOf(selected.key)?.problem"
          />
          <div v-for="(x, i) in selected.extracts ?? []" :key="`x${i}`" class="card">
            <span class="fact dim">Read from {{ x.source }}{{ x.hidden ? `, hidden in ${x.hidden}` : '' }}</span>
            <span class="mono raw"
              >{{ parts(x.text, x.marked ?? '')[0] }}<strong>{{ parts(x.text, x.marked ?? '')[1] }}</strong
              >{{ parts(x.text, x.marked ?? '')[2] }}</span
            >
            <div class="cites">
              <div class="chip" @click="open(x.ref)">
                <span class="chiptext">{{ cite(x.ref) }}</span>
              </div>
            </div>
          </div>
          <div v-if="selected.proof" class="card">
            <span class="fact dim">Encoded text as it appeared, in {{ selected.proof.encoding }}</span>
            <span class="mono raw">{{ selected.proof.encoded }}</span>
            <template v-if="selected.proof.stored_hash">
              <span class="fact dim">Hash stored for the value when the session began</span>
              <span class="mono raw">{{ selected.proof.stored_hash }}</span>
            </template>
            <template v-if="selected.proof.decoded_hash">
              <span class="fact dim">Hash of what decoding the text gave</span>
              <span class="mono raw">{{ selected.proof.decoded_hash }}</span>
            </template>
            <span :class="selected.proof.equal ? 'state' : 'state bad'">{{ selected.proof.equal ? 'The two are equal' : 'Not shown to be equal' }}</span>
            <div class="cites">
              <div v-for="r in selected.proof.refs" :key="cite(r)" class="chip" @click="open(r)">
                <span class="chiptext">{{ cite(r) }}</span>
              </div>
            </div>
          </div>

          <span class="label">STATEMENTS</span>
          <div v-for="(s, i) in selected.statements" :key="i" class="statement">
            <span class="kind">{{ s.kind }}</span>
            <span class="fact">{{ s.text }}</span>
            <div class="cites">
              <span v-if="s.rule" class="mono rulename">rule {{ s.rule }}</span>
              <div v-for="r in s.refs" :key="cite(r)" class="chip" @click="open(r)">
                <span class="chiptext">{{ cite(r) }}</span>
              </div>
            </div>
          </div>
          <template v-if="selected.narrative">
            <div v-for="(s, i) in selected.narrative.sentences" :key="`n${i}`" class="statement">
              <span class="kind">Model</span>
              <span class="fact">{{ s.text }}</span>
              <div class="cites">
                <div v-for="r in s.refs" :key="cite(r)" class="chip" @click="open(r)">
                  <span class="chiptext">{{ cite(r) }}</span>
                </div>
              </div>
            </div>
            <span class="fact dim"
              >Model is {{ selected.narrative.model }}. Its sentences are not evidence. {{ selected.narrative.note ?? '' }}
              {{ selected.narrative.removed > 0 ? `${selected.narrative.removed} removed for citing nothing it was given.` : '' }} It was given
              {{ selected.narrative.given.map(cite).join(' ') }}.</span
            >
          </template>
          <div v-else class="askrow">
            <span class="fact dim grow">{{
              data.model_note ? data.model_note : reviewer === '' ? 'No local model is set up. Nothing above needs one.' : 'The local model can add an account in prose.'
            }}</span>
            <button v-if="reviewer !== ''" :disabled="writing" @click="write">{{ writing ? 'Writing…' : 'Write it' }}</button>
          </div>

          <div v-if="rest.length > 0" class="line foldline" @click="fold('entries')">
            <span class="grow">{{ shown.includes('entries') ? 'Hide' : 'Show' }} all {{ selected.refs.length }} entries</span>
          </div>
          <template v-if="shown.includes('entries')">
            <RawEntry
              v-for="r in rest"
              :key="cite(r)"
              :cite="cite(r)"
              :type="entryOf(r)?.type ?? ''"
              :time="entryOf(r)?.time ?? ''"
              :data="entryOf(r)?.data ?? ''"
              :hash="r.hash"
              :mark="r.mark ?? ''"
              :problem="entryOf(r)?.problem"
            />
          </template>

          <span class="label">TIMELINE</span>
          <div v-for="t in timelines" :key="t.label" class="timeline">
            <span class="fact dim">{{ t.label }}, entries {{ t.from }} to {{ t.to }} of {{ t.total }}</span>
            <div class="ticks">
              <div
                v-for="i in t.items"
                :key="i.seq"
                :class="[i.findings.includes(selected.id) ? 'tick on' : 'tick', i.bad ? 'broken' : '', opened !== null && opened.label === t.label && opened.seq === i.seq ? 'open' : '']"
                :title="`${t.label}#${i.seq} ${i.label}`"
                @click="openItem(t.label, t.session, i.seq)"
              ></div>
            </div>
            <div v-for="i in t.items.filter((x) => x.findings.includes(selected!.id))" :key="`m${i.seq}`" class="tl" @click="openItem(t.label, t.session, i.seq)">
              <span class="mono tlseq">{{ t.label }}#{{ i.seq }}</span>
              <span class="mono tltime">{{ i.time.slice(11, 19) }}</span>
              <span class="fact grow">{{ i.label }}</span>
            </div>
          </div>

          <div v-if="(selected.links ?? []).length > 0" class="line foldline" @click="fold('links')">
            <span class="grow">Links</span>
            <span class="show">{{ shown.includes('links') ? 'Hide' : 'Show' }}</span>
          </div>
          <template v-if="shown.includes('links')">
            <div v-for="(l, i) in selected.links ?? []" :key="i" class="link" @click="open(l.refs[0])">
              <div class="node">
                <span class="nodekind">{{ l.from.kind }}</span>
                <span class="nodename">{{ l.from.name }}</span>
              </div>
              <div class="via">
                <span class="viatext">{{ l.via }}</span>
                <span class="mono stepmeta">{{ l.refs.map(cite).join(' ') }}</span>
              </div>
              <div class="node">
                <span class="nodekind">{{ l.to.kind }}</span>
                <span class="nodename">{{ l.to.name }}</span>
              </div>
            </div>
          </template>

          <div class="line foldline" @click="fold('how')">
            <span class="grow">How found</span>
            <span class="show">{{ shown.includes('how') ? 'Hide' : 'Show' }}</span>
          </div>
          <template v-if="shown.includes('how')">
            <div v-for="(h, i) in selected.how" :key="i" class="statement">
              <span class="kind">{{ i + 1 }}. {{ h.stage }}</span>
              <span class="fact">{{ h.what }}</span>
              <div class="cites">
                <div v-for="r in h.refs" :key="cite(r)" class="chip" @click="open(r)">
                  <span class="chiptext">{{ cite(r) }}</span>
                </div>
              </div>
            </div>
          </template>
        </div>
      </div>
      <!-- an opened entry stays in view whatever the finding is scrolled to -->
      <div v-if="opened !== null" class="entrybar">
        <div class="top">
          <div class="close" title="Close the entry" @click="opened = null">
            <Glyph lucide="x" :size="16" :stroke-width="1.5" />
          </div>
        </div>
        <RawEntry
          :cite="`${opened.label}#${opened.seq}`"
          :type="opened.type"
          :time="opened.time"
          :data="opened.data"
          :hash="opened.hash"
          :prev="opened.prev"
          :mark="openedMark"
          :problem="opened.problem"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.inv {
  display: flex;
  flex: 1;
  min-height: 0;
}
.inv.column {
  flex-direction: column;
}
.list {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.list.open {
  flex: none;
  width: var(--list-width);
}
.detail {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
/* the one line: between the list and the open finding */
.divider {
  width: 1px;
  background: var(--line);
}
.scroll {
  flex: 1;
  overflow: auto;
  min-height: 0;
}
.col {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: var(--col-width);
  margin: 0 auto;
  padding: var(--s24) var(--s20) var(--s40);
}
.col.wide {
  max-width: var(--detail-width);
}
.status {
  font-size: var(--fs-status);
  line-height: 1.2;
  font-weight: 500;
  letter-spacing: -0.4px;
  margin-bottom: var(--s12);
}
.title {
  font-size: var(--fs-h1);
  line-height: 1.25;
  font-weight: 500;
  letter-spacing: -0.3px;
  margin-top: var(--s16);
  margin-bottom: var(--s4);
}
.scope {
  align-self: flex-start;
  margin-bottom: var(--s4);
}
.label {
  font-size: var(--fs-tiny);
  letter-spacing: 0.8px;
  color: var(--sec);
  margin-top: var(--s32);
  margin-bottom: var(--s8);
}
/* a raw line, an excerpt or a hash: code, on the tone of a code block */
.card {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  background: var(--raise);
  border-radius: var(--r-row);
  padding: var(--s12);
  margin-bottom: var(--s8);
}
/* the opened entry: fixed under the pane, so it is in view wherever the pane is scrolled to */
.entrybar {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  background: var(--raise);
  padding: var(--s12) var(--s20);
}
.record,
.plain {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  margin-bottom: var(--s8);
}
.line {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s12);
  min-height: var(--row-min);
}
.foldline {
  margin-top: var(--s16);
}
.grow {
  flex: 1;
  min-width: 0;
}
.end {
  text-align: right;
}
.mono {
  font-family: var(--mono);
  font-size: var(--fs-mono);
}
.strong {
  font-weight: 500;
}
.dim,
.show,
.edge {
  color: var(--sec);
}
.show {
  font-size: var(--fs-small);
  text-decoration: underline;
}
.fact {
  font-size: var(--fs-small);
  line-height: 1.5;
  margin-top: var(--s4);
}
.limit {
  margin-bottom: var(--s4);
}
.cmd {
  margin-top: var(--s4);
  margin-bottom: var(--s8);
}
.raw {
  margin-top: var(--s8);
  margin-bottom: var(--s4);
  line-height: 1.5;
}
.state {
  font-size: var(--fs-small);
  font-weight: 500;
}
/* a failed check is a danger: a record that cannot be trusted is a risk, and is coloured as one */
.bad {
  color: var(--high);
}
/* the one word that says a record holds */
.ok {
  color: var(--ok);
}
.words {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s4);
}
.note {
  font-size: var(--fs-small);
  font-weight: 500;
  margin-top: var(--s4);
}
.problem {
  font-weight: 500;
  font-size: var(--fs-small);
  margin-bottom: var(--s8);
}
/* the edge of a field has to be seen, so it is not the hairline */
.askrow input {
  border: 1px solid var(--sec);
}
.askrow {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s8);
  margin-bottom: var(--s8);
}
.row {
  display: flex;
  flex-shrink: 0;
  align-items: flex-start;
  gap: var(--s12);
  padding: var(--s8) var(--s12);
  border-radius: var(--r-row);
  min-height: var(--row-min);
  transition: background-color 120ms ease-out;
}
.row:hover {
  background: var(--raise);
}
.row.current {
  background: var(--raise);
}
.rowtext {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}
.rowtitle {
  font-size: var(--fs);
  line-height: 1.4;
}
.rowmeta {
  font-size: var(--fs-small);
  color: var(--sec);
  margin-top: var(--s4);
}
.rowmeta.bad {
  color: var(--high);
}
.matrix {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}
.mrow {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--s4);
  margin-bottom: var(--s4);
}
.mhead {
  flex: 1;
  min-width: 0;
  font-size: var(--fs-tiny);
  color: var(--sec);
  text-align: center;
}
.mhead.first {
  flex: none;
  width: var(--s32);
  text-align: left;
}
.mcell {
  display: flex;
  flex: 1;
  min-width: 0;
  height: var(--s32);
  align-items: center;
  justify-content: center;
  border-radius: var(--s4);
}
/* a cell with findings in it is the only kind that can be pressed */
.mcell.has {
  background: var(--raise);
}
.mcell.on {
  border: 1px solid var(--accent);
}
.chain {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  align-items: stretch;
}
/* an arrow stays with the step it leads to when the chain wraps */
.hop {
  display: flex;
  align-items: center;
  margin-bottom: var(--s12);
}
.arrow {
  color: var(--sec);
  margin-left: var(--s12);
  margin-right: var(--s12);
}
.step {
  display: flex;
  flex-direction: column;
  max-width: var(--step-width);
  transition: opacity 120ms ease-out;
}
.step:active {
  opacity: 0.6;
}
.steptext {
  font-size: var(--fs);
  line-height: 1.35;
}
.stepmeta {
  font-size: var(--fs-tiny);
  color: var(--accent);
  text-decoration: underline;
  margin-top: var(--s4);
}
.step.rule .steptext,
.step.rule .stepmeta {
  color: var(--sec);
}
/* the name of a rule is not a link */
.step.rule .stepmeta {
  text-decoration: none;
}
.statement {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  margin-bottom: var(--s16);
}
.kind {
  font-size: var(--fs-small);
  font-weight: 500;
}
.cites {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s12);
  margin-top: var(--s4);
}
.rulename {
  font-size: var(--fs-tiny);
  color: var(--sec);
}
/* a reference to an entry is a link: it opens the raw line */
.chip {
  display: flex;
  align-items: center;
  min-height: var(--s24);
  transition: opacity 120ms ease-out;
}
.chip:active {
  opacity: 0.6;
}
.chiptext {
  font-family: var(--mono);
  font-size: var(--fs-tiny);
  color: var(--accent);
  text-decoration: underline;
}
.timeline {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  margin-bottom: var(--s12);
}
.ticks {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  gap: var(--s4);
  margin-top: var(--s8);
  margin-bottom: var(--s8);
}
.tick {
  width: var(--s8);
  height: var(--s20);
  border-radius: var(--s4);
  background: var(--line);
}
.tick.on {
  background: var(--accent);
}
/* an entry that fails its check: ink, where the finding's own entries are the accent */
.tick.broken {
  background: var(--text);
}
.tick.open {
  height: var(--s32);
}
.tl {
  display: flex;
  flex-shrink: 0;
  align-items: baseline;
  gap: var(--s12);
  min-height: var(--s32);
}
.tlseq {
  width: var(--seq-width);
  color: var(--accent);
  text-decoration: underline;
}
.tltime {
  width: var(--time-width);
  color: var(--sec);
}
.link {
  display: flex;
  flex-shrink: 0;
  align-items: flex-start;
  gap: var(--s12);
  margin-bottom: var(--s16);
}
.node {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}
.nodekind {
  font-size: var(--fs-tiny);
  color: var(--sec);
}
.nodename {
  font-size: var(--fs-small);
  line-height: 1.35;
}
.via {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: var(--via-width);
}
.viatext {
  font-size: var(--fs-tiny);
  color: var(--sec);
  text-align: center;
}
/* the chain: one step to a line, numbered, its entries at the right edge */
.stepline {
  display: flex;
  flex-shrink: 0;
  align-items: flex-start;
  gap: var(--s12);
  min-height: var(--s32);
  margin-bottom: var(--s4);
}
.num {
  width: var(--s20);
  color: var(--sec);
}
.cites.tight {
  margin-top: 0;
  justify-content: flex-end;
}
.top {
  display: flex;
  flex-direction: row;
  flex-shrink: 0;
  justify-content: flex-end;
}
.top.alone {
  justify-content: flex-start;
}
.close,
.back {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: center;
  gap: var(--s8);
  min-width: 44px;
  min-height: 44px;
  border-radius: var(--r-row);
  color: var(--sec);
}
.back {
  padding: 0 var(--s12);
}
.close:hover,
.back:hover {
  background: var(--raise);
}
</style>
