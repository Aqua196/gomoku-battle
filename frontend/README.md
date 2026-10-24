# frontend — Gomoku Battle SPA

React 18 + Vite + TypeScript. All WebSocket message types live in `src/ws/protocol.ts` and are aligned field-by-field with `../PROTOCOL.md` (frozen contract v1).

## Scripts

```bash
npm install
npm run dev        # vite dev server; proxies /ws /healthz /metrics → http://localhost:8080
npm run build      # tsc -b && vite build → dist/
npm run typecheck  # tsc -b
npm run lint       # eslint
npm test           # vitest (protocol parser / backoff / reducer / win line / pages)
```

## Layout

- `src/ws/protocol.ts` — C→S / S→C message types, Room, GameState, error codes
- `src/ws/client.ts` — WS client: hello, exponential-backoff reconnect (hello carries stored token), app-level ping, message dispatch
- `src/state/store.tsx` — React context + reducer: session / lobby / game state machine
- `src/components/Rail.tsx` — app shell rail (left on desktop, top bar on phones): seal, vertical title, live `/metrics` count, toggles
- `src/components/InkScape.tsx` — generative ink-wash landscape on a canvas (fBm ridgelines, dry-brush contours, moss dots, drifting mist, pointer parallax; pauses off-screen, still under reduced motion)
- `src/components/Board.tsx` — the board: hand-drawn (seeded) grid, ink stones, ripple on the newest stone, vermilion stroke through a five, move numbers; arrow keys + Enter to play, two-tap confirm on touch screens
- `src/components/InkStone.tsx`, `Seal.tsx` — procedurally shaped ink stones and vermilion seals (stable per board point)
- `src/pages/Gate.tsx` — landing page: title, name entry and a self-playing demo board (`components/HeroBoard.tsx`) over the landscape
- `src/pages/Lobby.tsx` — quick match, AI difficulty, create room, room table
- `src/pages/Game.tsx` — board, player seats with incense-stick turn clocks, turn status, resign, unrolling result scroll, final-board review
- `src/lib/` — pure helpers: seeded noise and ink paths (`ink.ts`), win-line detection, board notation, device prefs, WebAudio sound effects
- `src/styles/` — design tokens (rice paper / night, ink tones, one vermilion) and per-area stylesheets

### Fonts

- Chinese serif: Noto Serif SC (SIL OFL 1.1) subset to the characters the source contains, ~48 KB per weight in `src/assets/fonts/`. After changing UI copy, regenerate:

  ```bash
  npm pack @fontsource/noto-serif-sc@5.3.1 && tar xzf fontsource-noto-serif-sc-5.3.1.tgz
  pip install fonttools brotli && python3 scripts/subset-serif-font.py package
  ```

- Latin italic (Cormorant Garamond) and mono (JetBrains Mono): self-hosted from `@fontsource`, Latin subset only. User-supplied text (nicks, room names) uses the system sans stack.

`nick` / `token` persist in `localStorage` (`gomoku.nick` / `gomoku.token`), UI preferences in `gomoku.theme` / `gomoku.sound` / `gomoku.showOrder`.

In dev builds, `window.__gomoku(serverMsg)` injects a server message into the store for manual testing without a backend.
