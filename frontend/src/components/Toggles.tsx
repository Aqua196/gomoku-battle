import { usePrefs } from '../hooks/usePrefs'
import { effectiveTheme, setPref } from '../lib/prefs'
import { Icon } from './Icon'

export function SoundToggle() {
  const { sound } = usePrefs()
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={sound ? '关闭音效' : '开启音效'}
      title={sound ? '关闭音效' : '开启音效'}
      onClick={() => setPref('sound', !sound)}
    >
      <Icon name={sound ? 'volume' : 'mute'} />
    </button>
  )
}

export function ThemeToggle() {
  const { theme } = usePrefs()
  const dark = effectiveTheme(theme) === 'dark'
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={dark ? '切换到浅色' : '切换到深色'}
      title={dark ? '切换到浅色' : '切换到深色'}
      onClick={() => setPref('theme', dark ? 'light' : 'dark')}
    >
      <Icon name={dark ? 'sun' : 'moon'} />
    </button>
  )
}
