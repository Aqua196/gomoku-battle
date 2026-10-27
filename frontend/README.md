# frontend — Gomoku Battle SPA

React 18 + Vite + TypeScript. All WebSocket message types live in `src/ws/protocol.ts` and are aligned field-by-field with `../PROTOCOL.md` (v1.1: v1 plus backward-compatible additions — AI seat choice, spectating, game records, `welcome.roomId`).

## Scripts

```bash
npm install
npm run dev        # vite dev server; proxies /ws /api /healthz /metrics → http://localhost:8080
npm run build      # tsc -b && vite build → dist/
npm run typecheck  # tsc -b
npm run lint       # eslint
npm test           # vitest (protocol parser / backoff / reducer / win line / pages / replay / font subset coverage)
```

## Layout

- `src/ws/protocol.ts` — C→S / S→C message types, Room, GameState, error codes
- `src/ws/client.ts` — WS client: hello, exponential-backoff reconnect (hello carries stored token), app-level ping, message dispatch
- `src/state/reducer.ts` — the session / lobby / game (incl. spectating) state machine, pure and unit-tested
- `src/state/store.tsx` — the provider: WS client wiring, resume watchdog (1.5s, or 15s when `welcome.roomId` says a game is being restored), re-spectate after reconnect
- `src/state/useStore.ts` — context, `useStore` hook and the `StoreApi` actions
- `src/components/Rail.tsx` — app shell rail (left on desktop, top bar on phones): seal, vertical title, live `/metrics` count, toggles
- `src/components/InkScape.tsx` — generative ink-wash landscape on a canvas (fBm ridgelines, dry-brush contours, moss dots, drifting mist, pointer parallax; pauses off-screen, still under reduced motion)
- `src/components/Board.tsx` — the board: hand-drawn (seeded) grid, ink stones, ripple on the newest stone, vermilion stroke through a five, move numbers; arrow keys + Enter to play, two-tap confirm on touch screens
- `src/components/InkStone.tsx`, `Seal.tsx` — procedurally shaped ink stones and vermilion seals (stable per board point)
- `src/pages/Gate.tsx` — landing page: title, name entry and a self-playing demo board (`components/HeroBoard.tsx`) over the landscape
- `src/pages/Lobby.tsx` — quick match, AI seat (执黑 / 执白 / 猜先) and difficulty, create room, room table with 观战, records (mine / recent)
- `src/pages/Game.tsx` — board, player seats with incense-stick turn clocks, turn status, resign, unrolling result scroll, final-board review; spectator mode (turn by colour, 退出观战, neutral verdict)
- `src/pages/Replay.tsx` — `/replay/:id`: a recorded game stepped stone by stone (transport, scrubber, autoplay, keyboard, move list, share link); works without signing in
- `src/lib/` — pure helpers: seeded noise and ink paths (`ink.ts`), win-line detection, board notation, device prefs, WebAudio sound effects, record API (`records.ts`)
- `src/styles/` — design tokens (rice paper / night, ink tones, one vermilion) and per-area stylesheets

### Fonts

- Chinese serif: Noto Serif SC (SIL OFL 1.1) subset to the characters the source contains, ~56 KB per weight in `src/assets/fonts/`. After changing UI copy, regenerate (`src/lib/fontSubset.test.ts` fails in CI if you forget):

  ```bash
  npm pack @fontsource/noto-serif-sc@5.3.1 && tar xzf fontsource-noto-serif-sc-5.3.1.tgz
  pip install fonttools brotli && python3 scripts/subset-serif-font.py package
  ```

- Latin italic (Cormorant Garamond) and mono (JetBrains Mono): self-hosted from `@fontsource`, Latin subset only. User-supplied text (nicks, room names) uses the system sans stack.

`nick` / `token` persist in `localStorage` (`gomoku.nick` / `gomoku.token`), UI preferences in `gomoku.theme` / `gomoku.sound` / `gomoku.showOrder` / `gomoku.aiSide`.

In dev builds, `window.__gomoku(serverMsg)` injects a server message into the store for manual testing without a backend.
