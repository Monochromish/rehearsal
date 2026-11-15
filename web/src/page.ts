// What the screens need from where they run, done with what a browser has:
// the settings they start from, a media query, and keyboard shortcuts.

import { onUnmounted, ref } from 'vue'
import type { Ref } from 'vue'

export interface Settings {
  // where rehearse is serving the session
  url: string
  // a scripted session to show in place of a live one
  fixture: string
  // which item, tab and view to open on, for looking at one state on its own
  select: string
  tab: string
  view: string
  // 'light' or 'dark' to fix the theme; anything else follows the system
  theme: string
}

/** The page's own origin, and the rest from its query string. */
export function settings(): Settings {
  const query = new URLSearchParams(window.location.search)
  const get = (name: string): string => query.get(name) ?? ''
  return { url: window.location.origin, fixture: get('fixture'), select: get('select'), tab: get('tab'), view: get('view'), theme: get('theme') }
}

/** Whether a media query matches, kept current. */
export function useMediaQuery(query: string): Ref<boolean> {
  const list = window.matchMedia(query)
  const matches = ref(list.matches)
  const changed = (): void => {
    matches.value = list.matches
  }
  list.addEventListener('change', changed)
  onUnmounted(() => list.removeEventListener('change', changed))
  return matches
}

/**
 * Calls handler when key is pressed, until the component is unmounted. A
 * key typed into a text field is the field's. Space and Enter on a button
 * are the button's. A key held down counts once unless repeat is set.
 */
export function useShortcut(key: string, handler: () => void, options: { repeat?: boolean } = {}): void {
  const want = key === 'Space' ? ' ' : key
  const pressed = (event: KeyboardEvent): void => {
    if (event.key !== want || event.ctrlKey || event.metaKey || event.altKey) return
    if (event.repeat && options.repeat !== true) return
    const target = event.target
    if (target instanceof HTMLElement) {
      if (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return
      if (target.tagName === 'BUTTON' && (want === ' ' || want === 'Enter')) return
    }
    event.preventDefault()
    handler()
  }
  window.addEventListener('keydown', pressed)
  onUnmounted(() => window.removeEventListener('keydown', pressed))
}
