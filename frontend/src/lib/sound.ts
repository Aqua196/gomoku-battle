/**
 * Synthesized sound effects (WebAudio, no audio assets): a stone landing on
 * wood, a countdown tick and short win/lose cues. Silently no-ops where
 * WebAudio is missing (tests, old browsers) or when sound is turned off.
 */
import { getPrefs } from './prefs'

type Ctor = typeof AudioContext

let ctx: AudioContext | null = null

function audio(): AudioContext | null {
  if (!getPrefs().sound || typeof window === 'undefined') return null
  const AC: Ctor | undefined =
    window.AudioContext ?? (window as unknown as { webkitAudioContext?: Ctor }).webkitAudioContext
  if (!AC) return null
  try {
    ctx ??= new AC()
    if (ctx.state === 'suspended') void ctx.resume()
    return ctx
  } catch {
    return null
  }
}

function envelope(ac: AudioContext, peak: number, attack: number, decay: number, at = ac.currentTime) {
  const g = ac.createGain()
  g.gain.setValueAtTime(0.0001, at)
  g.gain.exponentialRampToValueAtTime(peak, at + attack)
  g.gain.exponentialRampToValueAtTime(0.0001, at + attack + decay)
  g.connect(ac.destination)
  return g
}

function tone(ac: AudioContext, freq: number, at: number, dur: number, peak: number, type: OscillatorType = 'sine') {
  const osc = ac.createOscillator()
  osc.type = type
  osc.frequency.setValueAtTime(freq, at)
  osc.connect(envelope(ac, peak, 0.012, dur, at))
  osc.start(at)
  osc.stop(at + dur + 0.05)
}

/** Hard click of a slate stone on a wooden board: filtered noise + low thump. */
function stone() {
  const ac = audio()
  if (!ac) return
  const t = ac.currentTime
  const len = Math.floor(ac.sampleRate * 0.07)
  const buf = ac.createBuffer(1, len, ac.sampleRate)
  const data = buf.getChannelData(0)
  for (let i = 0; i < len; i++) data[i] = (Math.random() * 2 - 1) * (1 - i / len) ** 4
  const src = ac.createBufferSource()
  src.buffer = buf
  const band = ac.createBiquadFilter()
  band.type = 'bandpass'
  band.frequency.value = 2600
  band.Q.value = 1.1
  src.connect(band)
  band.connect(envelope(ac, 0.9, 0.002, 0.07, t))
  src.start(t)

  const thump = ac.createOscillator()
  thump.frequency.setValueAtTime(190, t)
  thump.frequency.exponentialRampToValueAtTime(70, t + 0.09)
  thump.connect(envelope(ac, 0.35, 0.004, 0.1, t))
  thump.start(t)
  thump.stop(t + 0.15)
}

function tick() {
  const ac = audio()
  if (!ac) return
  tone(ac, 1320, ac.currentTime, 0.05, 0.12, 'triangle')
}

function start() {
  const ac = audio()
  if (!ac) return
  const t = ac.currentTime
  tone(ac, 523.25, t, 0.18, 0.14, 'triangle')
  tone(ac, 783.99, t + 0.09, 0.26, 0.12, 'triangle')
}

function win() {
  const ac = audio()
  if (!ac) return
  const t = ac.currentTime
  ;[523.25, 659.25, 783.99, 1046.5].forEach((f, i) => tone(ac, f, t + i * 0.1, 0.5, 0.16, 'triangle'))
  tone(ac, 1567.98, t + 0.42, 0.9, 0.06)
}

function lose() {
  const ac = audio()
  if (!ac) return
  const t = ac.currentTime
  tone(ac, 392, t, 0.35, 0.14, 'triangle')
  tone(ac, 311.13, t + 0.22, 0.6, 0.12, 'triangle')
}

export const sfx = { stone, tick, start, win, lose }
