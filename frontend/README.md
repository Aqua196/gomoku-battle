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
- `src/components/Rail.tsx` — app shell rail (left on desktop, top bar on phones): brand, live `/metrics` counter, connection, toggles
- `src/pages/Gate.tsx` — landing page: system spec sheet + callsign entry beside a self-playing demo board (`components/HeroBoard.tsx`)
- `src/pages/Lobby.tsx` — quick match (wait timer + queue position), AI difficulty rows, create room, room table
- `src/pages/Game.tsx` — board, player spec panels with segmented turn clocks, turn status, resign, result slab, final-board review
- `src/components/Board.tsx` — flat technical board: SVG grid, stones, last-move reticle, win line, move numbers; arrow keys + Enter to play, two-tap confirm on touch screens
- `src/lib/` — pure helpers: win-line detection, board notation, device prefs (theme / sound / move numbers), WebAudio sound effects
- `src/styles/` — design tokens (industrial light / dark themes, one signal yellow, square corners) and per-area stylesheets

Latin display (Barlow Condensed) and mono (JetBrains Mono) fonts are self-hosted from `@fontsource` (Latin subset only); Chinese uses the system sans stack.

`nick` / `token` persist in `localStorage` (`gomoku.nick` / `gomoku.token`), UI preferences in `gomoku.theme` / `gomoku.sound` / `gomoku.showOrder`.

In dev builds, `window.__gomoku(serverMsg)` injects a server message into the store for manual testing without a backend.
