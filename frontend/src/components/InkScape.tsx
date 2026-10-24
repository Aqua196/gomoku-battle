import { useEffect, useRef } from 'react'
import { fbm, rng } from '../lib/ink'
import { subscribePrefs } from '../lib/prefs'

/*
 * Generative ink-wash landscape (水墨山水) on a canvas: far-to-near ridgelines
 * from fractal noise, washed fills with 皴-style texture strokes, drifting mist
 * and a vermilion sun. Layers are pre-rendered off-screen; each frame only
 * composites them with pointer parallax, so the loop stays cheap. Pauses when
 * hidden or off-screen; renders a single still frame under reduced motion.
 */

interface LayerSpec {
  base: number // ridge baseline, fraction of height
  amp: number // ridge amplitude, fraction of height
  freq: number // ridge frequency across the width
  alpha: number // ink density
  depth: number // parallax strength
}

const LAYERS: LayerSpec[] = [
  { base: 0.64, amp: 0.4, freq: 1.3, alpha: 0.07, depth: 0.12 },
  { base: 0.74, amp: 0.36, freq: 1.8, alpha: 0.12, depth: 0.25 },
  { base: 0.86, amp: 0.32, freq: 2.3, alpha: 0.2, depth: 0.45 },
  { base: 1.0, amp: 0.28, freq: 3.0, alpha: 0.32, depth: 0.75 },
]

const MARGIN = 48 // px of overscan each side so parallax never shows an edge
const DEFAULT_SUN = { x: 0.62, y: 0.075, r: 0.065 }

export interface InkScapeProps {
  seed?: number
  /** sun centre and radius as fractions of the canvas (radius of min side) */
  sun?: { x: number; y: number; r: number } | null
  /** shift all ridgelines down (+) or up (−), fraction of height */
  lift?: number
  className?: string
}

function cssTriplet(el: Element, name: string, fallback: string): string {
  const v = getComputedStyle(el).getPropertyValue(name).trim()
  return (v || fallback).split(/[\s,]+/).join(',')
}

function reducedMotion(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

/** Pass `sun` as a stable (module-level) object: it is an effect dependency. */
export function InkScape({ seed = 7, sun = DEFAULT_SUN, lift = 0, className }: InkScapeProps) {
  const ref = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = ref.current
    // No canvas/observer support (e.g. jsdom in tests): stay a blank element.
    if (typeof ResizeObserver === 'undefined' || typeof IntersectionObserver === 'undefined') return
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx) return

    let w = 0
    let h = 0
    let dpr = 1
    let layers: HTMLCanvasElement[] = []
    let sunLayer: HTMLCanvasElement | null = null
    let ink = '27,26,23'
    let mistRgb = '242,237,226'
    let sealRgb = '184,58,44'
    let frame = 0
    let raf = 0
    let visible = true
    const pointer = { x: 0, y: 0, tx: 0, ty: 0 }
    const still = reducedMotion()

    // Peaks: low-frequency swell raised to a power (pointed summits) plus a
    // little higher-frequency detail for the brush wobble.
    const noise = (x: number) => fbm(x, seed * 7 + 1, 2)
    const ridge = (x: number, L: LayerSpec, i: number) => {
      const u = x / w
      // Stretch the noise to the full 0..1 range before shaping, so ranges
      // rise to real summits instead of rolling hills.
      const raw = fbm(u * L.freq, seed * 31 + i, 3) * 0.5 + 0.5
      const swell = Math.min(1, Math.max(0, (raw - 0.28) / 0.5))
      const detail = fbm(u * L.freq * 7 + 3.1, seed * 17 + i, 3) * 0.05
      const shape = Math.pow(swell, 1.45) + detail
      return h * (L.base + lift) - h * L.amp * shape
    }

    const renderLayers = () => {
      ink = cssTriplet(canvas, '--scape-ink', ink)
      mistRgb = cssTriplet(canvas, '--scape-mist', mistRgb)
      sealRgb = cssTriplet(canvas, '--scape-sun', sealRgb)
      layers = LAYERS.map((L, i) => {
        const c = document.createElement('canvas')
        c.width = Math.ceil((w + MARGIN * 2) * dpr)
        c.height = Math.ceil(h * dpr)
        const g = c.getContext('2d')!
        g.scale(dpr, dpr)
        g.translate(MARGIN, 0)

        const path = new Path2D()
        path.moveTo(-MARGIN, h)
        for (let x = -MARGIN; x <= w + MARGIN; x += 3) path.lineTo(x, ridge(x, L, i))
        path.lineTo(w + MARGIN, h)
        path.closePath()

        // Wash: dense along the ridge, the foot dissolving into mist, so
        // each range floats above the next (留白).
        const top = h * (L.base + lift - L.amp)
        const grad = g.createLinearGradient(0, top, 0, h * (L.base + lift + 0.02))
        grad.addColorStop(0, `rgba(${ink},${L.alpha})`)
        grad.addColorStop(0.45, `rgba(${ink},${L.alpha * 0.6})`)
        grad.addColorStop(1, `rgba(${ink},0)`)
        g.fillStyle = grad
        g.fill(path)

        // Dry-brush contour: the ridge retraced a few times, slightly offset.
        const rand = rng(seed * 101 + i)
        // The line fades out where the range sinks into the valley floor.
        const floor = h * (L.base + lift)
        for (let pass = 0; pass < 3; pass++) {
          const dy = (rand() - 0.3) * 2.2
          const strength = Math.min(0.85, L.alpha * (pass === 0 ? 1.6 : 0.7))
          g.lineWidth = pass === 0 ? 1.2 : 0.7
          let px = -MARGIN
          let py = ridge(px, L, i) + dy
          for (let x = -MARGIN + 4; x <= w + MARGIN; x += 4) {
            const y = ridge(x, L, i) + dy + noise(x * 0.05 + pass * 9) * 0.8
            const rise = Math.min(1, Math.max(0, ((floor - y) / (h * L.amp)) * 3))
            if (rise > 0.02) {
              g.strokeStyle = `rgba(${ink},${strength * rise})`
              g.beginPath()
              g.moveTo(px, py)
              g.lineTo(x, y)
              g.stroke()
            }
            px = x
            py = y
          }
        }

        // 苔点: moss dots clustered on the summits of the nearer ranges.
        if (i >= 1) {
          const dots = Math.round((w / 40) * i)
          for (let k = 0; k < dots; k++) {
            const x = rand() * (w + MARGIN * 2) - MARGIN
            const y0 = ridge(x, L, i)
            const height = h * (L.base + lift) - y0
            if (height < h * L.amp * 0.45) continue // summits only
            const y = y0 + 2 + rand() * 7
            g.fillStyle = `rgba(${ink},${L.alpha * (1.2 + rand())})`
            g.beginPath()
            g.ellipse(x, y, 0.8 + rand() * 1.8, 0.6 + rand() * 1.2, rand() * Math.PI, 0, Math.PI * 2)
            g.fill()
          }
        }
        return c
      })

      sunLayer = null
      if (sun) {
        const r = Math.min(w, h) * sun.r
        const c = document.createElement('canvas')
        c.width = c.height = Math.ceil(r * 2.4 * dpr)
        const g = c.getContext('2d')!
        g.scale(dpr, dpr)
        g.translate(r * 1.2, r * 1.2)
        g.fillStyle = `rgba(${sealRgb},0.9)`
        g.beginPath()
        g.arc(0, 0, r, 0, Math.PI * 2)
        g.fill()
        // Dry-brush speckle so the sun reads as pigment, not a flat disc.
        const rand = rng(seed * 7)
        g.globalCompositeOperation = 'destination-out'
        for (let k = 0; k < r * 6; k++) {
          const a = rand() * Math.PI * 2
          const d = Math.sqrt(rand()) * r
          g.fillStyle = `rgba(0,0,0,${0.08 + rand() * 0.25})`
          g.beginPath()
          g.arc(Math.cos(a) * d, Math.sin(a) * d, 0.4 + rand() * 1.4, 0, Math.PI * 2)
          g.fill()
        }
        sunLayer = c
      }
    }

    const mist = (t: number, band: number, count: number) => {
      const rand = rng(seed * 13 + band)
      for (let k = 0; k < count; k++) {
        const speed = 6 + rand() * 10 // px per second
        const span = w + 600
        const x = ((rand() * span + (t / 1000) * speed) % span) - 300
        const y = h * (LAYERS[band].base + lift - LAYERS[band].amp * 0.2) + (rand() - 0.5) * h * 0.08
        const rx = 160 + rand() * 260
        const ry = 24 + rand() * 40
        const g = ctx.createRadialGradient(x, y, 0, x, y, rx)
        g.addColorStop(0, `rgba(${mistRgb},0.55)`)
        g.addColorStop(1, `rgba(${mistRgb},0)`)
        ctx.save()
        ctx.translate(x, y)
        ctx.scale(1, ry / rx)
        ctx.translate(-x, -y)
        ctx.fillStyle = g
        ctx.fillRect(x - rx, y - rx, rx * 2, rx * 2)
        ctx.restore()
      }
    }

    const draw = (t: number) => {
      pointer.x += (pointer.tx - pointer.x) * 0.06
      pointer.y += (pointer.ty - pointer.y) * 0.06
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.clearRect(0, 0, w, h)
      if (sunLayer && sun) {
        const r = Math.min(w, h) * sun.r
        ctx.drawImage(
          sunLayer,
          w * sun.x - r * 1.2 + pointer.x * 8,
          h * sun.y - r * 1.2 + pointer.y * 5,
          sunLayer.width / dpr,
          sunLayer.height / dpr,
        )
      }
      layers.forEach((c, i) => {
        const L = LAYERS[i]
        ctx.drawImage(c, -MARGIN + pointer.x * L.depth * 40, pointer.y * L.depth * 14, c.width / dpr, c.height / dpr)
        if (i === 1) mist(t, 1, 4)
        if (i === 2) mist(t + 9000, 2, 3)
      })
    }

    const loop = (t: number) => {
      raf = requestAnimationFrame(loop)
      // ~30 fps is plenty for drifting mist.
      if (!visible || document.hidden || frame++ % 2) return
      draw(t)
    }

    const resize = () => {
      const rect = canvas.getBoundingClientRect()
      if (rect.width < 2 || rect.height < 2) return
      dpr = Math.min(2, window.devicePixelRatio || 1)
      w = rect.width
      h = rect.height
      canvas.width = Math.round(w * dpr)
      canvas.height = Math.round(h * dpr)
      renderLayers()
      draw(performance.now())
    }

    const onPointer = (e: PointerEvent) => {
      const rect = canvas.getBoundingClientRect()
      pointer.tx = Math.max(-0.5, Math.min(0.5, (e.clientX - rect.left) / rect.width - 0.5))
      pointer.ty = Math.max(-0.5, Math.min(0.5, (e.clientY - rect.top) / rect.height - 0.5))
    }

    const ro = new ResizeObserver(() => resize())
    ro.observe(canvas)
    const io = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting
    })
    io.observe(canvas)
    const recolor = () => {
      if (!w) return
      renderLayers()
      draw(performance.now())
    }
    const unsubscribe = subscribePrefs(recolor)
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    mq?.addEventListener?.('change', recolor)

    resize()
    if (!still) {
      window.addEventListener('pointermove', onPointer, { passive: true })
      raf = requestAnimationFrame(loop)
    }

    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      io.disconnect()
      unsubscribe()
      mq?.removeEventListener?.('change', recolor)
      window.removeEventListener('pointermove', onPointer)
    }
  }, [seed, sun, lift])

  return <canvas ref={ref} className={`inkscape${className ? ` ${className}` : ''}`} aria-hidden />
}
