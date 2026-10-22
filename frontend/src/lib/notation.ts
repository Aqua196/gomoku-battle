import { BOARD_SIZE } from '../ws/protocol'

/** Column letter A–O (x = 0 → A). */
export function colLabel(x: number): string {
  return String.fromCharCode(65 + x)
}

/** Row number counted from the bottom edge, as on a printed board. */
export function rowLabel(y: number, size = BOARD_SIZE): number {
  return size - y
}

/** Board coordinate such as H8 for the center point. */
export function pointLabel(x: number, y: number, size = BOARD_SIZE): string {
  return `${colLabel(x)}${rowLabel(y, size)}`
}
