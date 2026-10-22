from playwright.sync_api import sync_playwright
import json, pathlib, time
with sync_playwright() as p:
    browser = p.chromium.connect_over_cdp("http://localhost:29229")
    ctx = browser.new_context()
    pg = ctx.new_page()
    pg.goto("http://localhost:8080", timeout=15000)
    pg.wait_for_timeout(800)
    # nick gate
    nick = pg.locator('input[placeholder*="昵称"], input[placeholder*="nick" i]').first
    if nick.count():
        nick.fill("Alice")
        pg.locator('button:has-text("进入")').first.click()
        pg.wait_for_timeout(500)
    # 人机：点「困难」
    btn = pg.locator('button:has-text("困难")').first
    assert btn.count(), "no hard AI button"
    btn.click()
    pg.wait_for_timeout(1200)
    # on board page: play 6 moves wherever free near center
    moves = [(7,7),(8,7),(6,8),(9,6),(5,9),(10,5)]
    ai_replies = 0
    my_done = 0
    for i,(x,y) in enumerate(moves):
        cell = pg.locator(f'button.cell[aria-label="({x},{y})"]')
        if cell.count() == 0:
            print("cell missing", x, y); break
        try:
            cell.click(timeout=2000); my_done += 1
        except Exception as e:
            print("click fail", x, y, e); break
        pg.wait_for_timeout(2500)  # AI hard thinks <2s
        # count white stones
        ai_replies = pg.locator('button.cell .stone.white, .cell.white .stone, .cell .stone-w').count()
        print(f"after my move {i+1}: ai stones~={ai_replies}")
        over = pg.locator('text=获胜, text=胜利, text=game over, .overlay, .game-over').count()
        if over: print("game over overlay seen"); break
    # check thinking hint appeared at least transiently (poll) — skip if too fast
    badge = pg.locator('text=人机').count()
    print(f"RESULT my_moves={my_done} badge(人机)={badge}")
    pg.screenshot(path=str(pathlib.Path(__file__).with_name("m2-e2e-ai.png")), full_page=False)
    ctx.close()
