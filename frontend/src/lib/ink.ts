/**
 * Procedural ink: seeded randomness, value noise and the SVG path builders
 * behind the brush-like stones and hand-drawn grid. Everything is a pure
 * function of its seed, so a stone keeps the same shape across renders.
 */

/** mulberry32: tiny seeded PRNG returning floats in [0, 1). */
export function rng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

/** Integer hash → [0, 1). */
function hash(n: number, seed: number): number {
  let h = Math.imul(n ^ seed, 0x27d4eb2d)
  h ^= h >>> 15
  h = Math.imul(h, 0x85ebca6b)
  h ^= h >>> 13
  return (h >>> 0) / 4294967296
}

/** Smooth 1-D value noise in [-1, 1]. */
export function noise1(x: number, seed = 0): number {
  const i = Math.floor(x)
  const f = x - i
  const u = f * f * (3 - 2 * f)
  const a = hash(i, seed)
  const b = hash(i + 1, seed)
  return (a + (b - a) * u) * 2 - 1
}

/** Fractal (fBm) value noise in roughly [-1, 1]. */
export function fbm(x: number, seed = 0, octaves = 5): number {
  let sum = 0
  let amp = 0.5
  let freq = 1
  let norm = 0
  for (let o = 0; o < octaves; o++) {
    sum += amp * noise1(x * freq, seed + o * 101)
    norm += amp
    amp *= 0.5
    freq *= 2
  }
  return sum / norm
}

const f = (n: number) => n.toFixed(2)

/** Closed smooth path through points (Catmull-Rom converted to cubic Béziers). */
export function closedCurve(pts: readonly (readonly [number, number])[]): string {
  const n = pts.length
  let d = `M${f(pts[0][0])},${f(pts[0][1])}`
  for (let i = 0; i < n; i++) {
    const p0 = pts[(i - 1 + n) % n]
    const p1 = pts[i]
    const p2 = pts[(i + 1) % n]
    const p3 = pts[(i + 2) % n]
    const c1x = p1[0] + (p2[0] - p0[0]) / 6
    const c1y = p1[1] + (p2[1] - p0[1]) / 6
    const c2x = p2[0] - (p3[0] - p1[0]) / 6
    const c2y = p2[1] - (p3[1] - p1[1]) / 6
    d += `C${f(c1x)},${f(c1y)} ${f(c2x)},${f(c2y)} ${f(p2[0])},${f(p2[1])}`
  }
  return `${d}Z`
}

/**
 * Irregular ink blot centred on the origin: a circle of radius r whose edge
 * wanders by up to `rough` (fraction of r) through a few seeded harmonics.
 */
export function blobPath(seed: number, r: number, rough = 0.05, cx = 0, cy = 0, points = 28): string {
  const rand = rng(seed)
  const phases = [rand() * Math.PI * 2, rand() * Math.PI * 2, rand() * Math.PI * 2]
  const pts: [number, number][] = []
  for (let i = 0; i < points; i++) {
    const a = (i / points) * Math.PI * 2
    const wobble =
      Math.sin(3 * a + phases[0]) * 0.5 + Math.sin(5 * a + phases[1]) * 0.3 + Math.sin(8 * a + phases[2]) * 0.2
    const rr = r * (1 + rough * wobble + rough * 0.25 * (rand() - 0.5))
    pts.push([cx + Math.cos(a) * rr, cy + Math.sin(a) * rr])
  }
  return closedCurve(pts)
}

/**
 * Brush ring (a white stone's outline): an outer and an inner blob drawn as
 * one evenodd path, so the stroke thickens and thins like a loaded brush.
 */
export function ringPath(seed: number, r: number, width: number, rough = 0.035): string {
  return `${blobPath(seed, r, rough)} ${blobPath(seed + 7919, r - width, rough * 1.6)}`
}

/** Slightly wavering straight line from (x1,y1) to (x2,y2), as a path. */
export function wobbleLine(seed: number, x1: number, y1: number, x2: number, y2: number, amp: number, segments = 6): string {
  const dx = x2 - x1
  const dy = y2 - y1
  const len = Math.hypot(dx, dy) || 1
  const nx = -dy / len
  const ny = dx / len
  let d = `M${f(x1)},${f(y1)}`
  for (let i = 1; i <= segments; i++) {
    const t = i / segments
    const off = i === segments ? 0 : noise1(t * 3.1, seed) * amp
    d += `L${f(x1 + dx * t + nx * off)},${f(y1 + dy * t + ny * off)}`
  }
  return d
}

/** Stable per-point seed so each intersection keeps its own stone shape. */
export function pointSeed(x: number, y: number, salt = 0): number {
  return ((x + 1) * 73856093) ^ ((y + 1) * 19349663) ^ (salt * 83492791)
}
