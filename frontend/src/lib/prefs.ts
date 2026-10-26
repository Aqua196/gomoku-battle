/**
 * Per-device UI preferences (theme, sound, move numbers) kept in
 * localStorage and exposed as a tiny external store for useSyncExternalStore.
 * Storage can be unavailable (private mode, tests), so every access is guarded.
 */

export type ThemePref = 'light' | 'dark' | 'system'
/** Seat asked for in play_ai: 执黑 / 执白 / 猜先 (random). */
export type AiSide = 'black' | 'white' | 'random'

export interface Prefs {
  theme: ThemePref
  sound: boolean
  showOrder: boolean
  aiSide: AiSide
}

const KEYS: Record<keyof Prefs, string> = {
  theme: 'gomoku.theme',
  sound: 'gomoku.sound',
  showOrder: 'gomoku.showOrder',
  aiSide: 'gomoku.aiSide',
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* preference simply won't persist */
  }
}

function load(): Prefs {
  const theme = read(KEYS.theme)
  const side = read(KEYS.aiSide)
  return {
    theme: theme === 'light' || theme === 'dark' ? theme : 'system',
    sound: read(KEYS.sound) !== '0',
    showOrder: read(KEYS.showOrder) === '1',
    aiSide: side === 'white' || side === 'random' ? side : 'black',
  }
}

let prefs: Prefs = load()
const listeners = new Set<() => void>()

export function getPrefs(): Prefs {
  return prefs
}

export function subscribePrefs(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function setPref<K extends keyof Prefs>(key: K, value: Prefs[K]) {
  prefs = { ...prefs, [key]: value }
  write(KEYS[key], typeof value === 'boolean' ? (value ? '1' : '0') : String(value))
  if (key === 'theme') applyTheme(prefs.theme)
  listeners.forEach((fn) => fn())
}

/** Reflect the theme on <html data-theme>; "system" defers to the media query. */
export function applyTheme(theme: ThemePref) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  if (theme === 'system') delete root.dataset.theme
  else root.dataset.theme = theme
}

export function systemPrefersLight(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: light)').matches
}

export function effectiveTheme(theme: ThemePref): 'light' | 'dark' {
  if (theme !== 'system') return theme
  return systemPrefersLight() ? 'light' : 'dark'
}
