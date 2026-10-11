# frontend — Gomoku Battle SPA

React 18 + Vite + TypeScript. All WebSocket message types live in `src/ws/protocol.ts` and are aligned field-by-field with `../PROTOCOL.md` (frozen contract v1).

## Scripts

```bash
npm install
npm run dev        # vite dev server; proxies /ws /healthz /metrics → http://localhost:8080
npm run build      # tsc -b && vite build → dist/
npm run typecheck  # tsc -b
npm run lint       # eslint
npm test           # vitest (protocol parser / backoff / reducer state machine)
```

## Layout

- `src/ws/protocol.ts` — C→S / S→C message types, Room, GameState, error codes
- `src/ws/client.ts` — WS client: hello, exponential-backoff reconnect (hello carries stored token), app-level ping, message dispatch
- `src/state/store.tsx` — React context + reducer: session / lobby / game state machine
- `src/pages/Lobby.tsx` — nick gate, room list, create room, matchmaking, play_ai (M2 stub)
- `src/pages/Game.tsx` — 15×15 board, turn + reconnect countdowns, lastMove, presence, resign, game-over overlay

`nick` / `token` persist in `localStorage` (`gomoku.nick` / `gomoku.token`).
In dev builds, `window.__gomoku(serverMsg)` injects a server message into the store for manual testing without a backend.
