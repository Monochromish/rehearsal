// Colour means risk only: neutral greys and three state colours. They are
// defined here once, handed to the root element as CSS variables, and used
// nowhere else by value, so a different renderer can read the same palettes.

export type ThemeName = 'light' | 'dark'

export type Palette = Record<string, string>

export const palettes: Record<ThemeName, Palette> = {
  light: {
    '--bg': '#FFFFFF',
    '--sunken': '#F5F5F4',
    '--text': '#1A1A1A',
    '--secondary': '#5E5E5E',
    '--hairline': '#E2E2E0',
    '--high': '#B3261E',
    '--medium': '#8A5300',
    '--allowed': '#1B6E33',
  },
  dark: {
    '--bg': '#151515',
    '--sunken': '#1D1D1D',
    '--text': '#EDEDED',
    '--secondary': '#A6A6A6',
    '--hairline': '#2F2F2F',
    '--high': '#FF8A80',
    '--medium': '#E8B04A',
    '--allowed': '#6FCF8A',
  },
}

const dark = '(prefers-color-scheme: dark)'

export function systemTheme(): ThemeName {
  return window.matchMedia(dark).matches ? 'dark' : 'light'
}

/** Calls onChange when the system switches theme. Returns a function that stops it. */
export function watchSystemTheme(onChange: (theme: ThemeName) => void): () => void {
  const query = window.matchMedia(dark)
  const listener = (): void => onChange(query.matches ? 'dark' : 'light')
  query.addEventListener('change', listener)
  return () => query.removeEventListener('change', listener)
}
