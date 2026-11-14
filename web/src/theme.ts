// Every colour the page uses, defined here once, handed to the root
// element as CSS variables and used nowhere else by value, so a different
// renderer can read the same palettes.
//
// What each is for:
//   --window, --panel, --raised  surfaces. The page is one panel; --raised is
//                                for what sits on it (a command, a diff).
//   --text, --secondary          words. A button is ink: --text behind,
//                                --panel for its label.
//   --hairline                   decoration only. An edge that has to be
//                                seen, such as a button's outline, is
//                                --secondary.
//   --accent                     focus rings and links, and nothing else. It
//                                is close to the text colour, so a link is
//                                always underlined as well.
//   --high, --medium, --allowed  risk, and only on the words High, Medium
//                                and Allowed. Colour never carries a
//                                meaning by itself.

export type ThemeName = 'light' | 'dark'

export type Palette = Record<string, string>

export const palettes: Record<ThemeName, Palette> = {
  light: {
    '--window': '#e8eaec',
    '--panel': '#fcfcfd',
    '--raised': '#e1e3e5',
    '--text': '#171a1d',
    '--secondary': '#44474b',
    '--hairline': '#cdcfd2',
    '--accent': '#1f3d69',
    '--high': '#920a11',
    '--medium': '#8b5002',
    '--allowed': '#0b6f49',
  },
  dark: {
    '--window': '#0c0d0f',
    '--panel': '#161719',
    '--raised': '#252729',
    '--text': '#e8e9eb',
    '--secondary': '#b2b4b8',
    '--hairline': '#343538',
    '--accent': '#b5d0f5',
    '--high': '#f76565',
    '--medium': '#eab946',
    '--allowed': '#82e2ad',
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
