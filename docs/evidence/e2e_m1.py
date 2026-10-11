#!/usr/bin/env python3
"""M1 e2e: two real browser contexts play a full gomoku game via matchmaking."""
import json, sys, time, urllib.request
from playwright.sync_api import sync_playwright

BASE = "http://localhost:8080"

def log(*a): print("[e2e]", *a, flush=True)

def enter_lobby(page, nick):
    page.goto(BASE, wait_until="domcontentloaded")
    page.wait_for_selector('input[placeholder*="昵称"]', timeout=15000)
    page.fill('input[placeholder*="昵称"]', nick)
    page.click('button:has-text("进入大厅")')
    page.wait_for_selector('button:has-text("快速匹配")', timeout=15000)
    log(nick, "in lobby")

def main():
    with sync_playwright() as p:
        b = p.chromium.connect_over_cdp("http://localhost:29229")
        ctxA, ctxB = b.new_context(), b.new_context()
        pa, pb = ctxA.new_page(), ctxB.new_page()
        enter_lobby(pa, "Alice")
        enter_lobby(pb, "Bob")
        pa.click('button:has-text("快速匹配")')
        pb.click('button:has-text("快速匹配")')
        # both should land on game page
        pa.wait_for_selector('button.cell', timeout=15000)
        pb.wait_for_selector('button.cell', timeout=15000)
        time.sleep(1.0)
        # determine colors from footer text 你执黑/你执白
        def color(page):
            t = page.inner_text("body")
            return "black" if "你执黑" in t else ("white" if "你执白" in t else "?")
        ca, cb = color(pa), color(pb)
        log("colors:", ca, cb)
        pages = {"black": pa if ca == "black" else pb, "white": pb if cb == "white" else pa}
        seq = [("black",7,7),("white",14,7),("black",8,7),("white",14,8),
               ("black",9,7),("white",14,9),("black",10,7),("white",14,10),("black",11,7)]
        for side,x,y in seq:
            pg = pages[side]
            # wait until it's this side's turn: its cells become enabled
            pg.wait_for_selector('button.cell:not([disabled])', timeout=15000)
            pg.click(f'button.cell[aria-label="({x},{y})"]')
            log(f"{side} played ({x},{y})")
            time.sleep(0.35)
        # game over overlay on both
        pa.wait_for_selector('text=胜', timeout=15000)
        log("overlay on A:", pa.locator("body").inner_text()[:200].replace("\n"," | "))
        pa.screenshot(path="/tmp/e2e_gameover.png", full_page=False)
        m = json.load(urllib.request.urlopen(BASE+"/metrics"))
        log("metrics:", m)
        assert m["gamesPlayed"] >= 1, "gamesPlayed not incremented"
        log("E2E PASS: full game played to game_over in real browsers")
        ctxA.close(); ctxB.close()

if __name__ == "__main__":
    main()
