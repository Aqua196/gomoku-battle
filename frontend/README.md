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
- `src/pages/Gate.tsx` — landing page: nickname entry beside a self-playing 3D board (`components/HeroBoard.tsx`)
- `src/pages/Lobby.tsx` — quick match (radar + wait timer), AI difficulty tiles, create room, live room cards
- `src/pages/Game.tsx` — board, player panels with countdown rings, turn status, resign, result card, final-board review
- `src/components/Board.tsx` — wooden board: SVG grid, stones, last-move ripple, win line, move numbers; arrow keys + Enter to play, two-tap confirm on touch screens
- `src/lib/` — pure helpers: win-line detection, board notation, device prefs (theme / sound / move numbers), WebAudio sound effects
- `src/styles/` — design tokens (dark "ink" / light "paper" themes) and per-area stylesheets

`nick` / `token` persist in `localStorage` (`gomoku.nick` / `gomoku.token`), UI preferences in `gomoku.theme` / `gomoku.sound` / `gomoku.showOrder`.

### Heading font

Serif headings use Noto Serif SC (SIL OFL 1.1) subset to the fixed heading copy, ~9 KB per weight in `src/assets/fonts/`.
After adding serif text, add its characters to `CHARS` in `scripts/subset-serif-font.py` and regenerate:

```bash
npm pack @fontsource/noto-serif-sc@5.3.1 && tar xzf fontsource-noto-serif-sc-5.3.1.tgz
pip install fonttools brotli && python3 scripts/subset-serif-font.py package
```

In dev builds, `window.__gomoku(serverMsg)` injects a server message into the store for manual testing without a backend.
