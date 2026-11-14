// Colours are plain data handed to the root element as CSS variables, so a
// different renderer can read the same palettes.

export type ThemeName = 'light' | 'dark'

export type Palette = Record<string, string>

export const palettes: Record<ThemeName, Palette> = {
  light: {
    '--bg': '#f6f5f1',
    '--surface': '#ffffff',
    '--text': '#1b1b1f',
    '--muted': '#5c5f66',
    '--border': '#d9d7d0',
    '--live': '#1d7a46',
    '--real-bg': '#fdebc8',
    '--real-text': '#5c3400',
    '--real-border': '#c97a10',
    '--canary-bg': '#e8eaff',
    '--canary-text': '#262b85',
    '--canary-border': '#5560d6',
    '--flag-bg': '#ffe2dd',
    '--flag-text': '#8a1a0d',
    '--flag-border': '#d93a25',
  },
  dark: {
    '--bg': '#131417',
    '--surface': '#1d1f24',
    '--text': '#f1f1f3',
    '--muted': '#a6aab3',
    '--border': '#363940',
    '--live': '#6fd69b',
    '--real-bg': '#3d2c0c',
    '--real-text': '#ffd899',
    '--real-border': '#d98a1f',
    '--canary-bg': '#1f2350',
    '--canary-text': '#cdd2ff',
    '--canary-border': '#7b86ff',
    '--flag-bg': '#47150f',
    '--flag-text': '#ffbcb1',
    '--flag-border': '#ff6a55',
  },
}

export function preferredTheme(): ThemeName {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}
