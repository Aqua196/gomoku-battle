import { useSyncExternalStore } from 'react'
import { getPrefs, subscribePrefs } from '../lib/prefs'
import type { Prefs } from '../lib/prefs'

/** Live view of the device preferences (theme / sound / move numbers). */
export function usePrefs(): Prefs {
  return useSyncExternalStore(subscribePrefs, getPrefs, getPrefs)
}
