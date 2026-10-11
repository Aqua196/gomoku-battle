import websocket, json, time, sys, random
ws = websocket.create_connection("ws://localhost:8080/ws", timeout=10)
ws.send(json.dumps({"type":"hello","nick":"Tester"}))
r = json.loads(ws.recv()); print("welcome:", r["type"], "resumed=", r.get("resumed"), flush=True)
ws.send(json.dumps({"type":"play_ai","difficulty":"hard"}))
occupied=set()
my_turn=False
ai_moves=0; my_moves=0
deadline=time.time()+90
def play_move():
    global my_moves
    for _ in range(200):
        x,y=random.randint(2,12),random.randint(2,12)
        if (x,y) not in occupied:
            occupied.add((x,y)); my_moves+=1
            ws.send(json.dumps({"type":"move","x":x,"y":y}))
            return
    print("no free spot?!"); 
while time.time()<deadline:
    try: m=json.loads(ws.recv())
    except Exception as e: print("recv err",e); break
    t=m["type"]
    if t=="game_start":
        g=m["game"]; print("start: AI nick=",g["players"]["white"]["nick"],"isAI=",g["players"]["white"]["isAI"],"me=",m.get("youColor"),flush=True)
        if g["turn"]=="black": play_move()
    elif t=="game_state":
        g=m.get("game") or m
        lm=g.get("lastMove")
        if lm:
            occupied.add((lm["x"],lm["y"]))
            if lm.get("color")=="white" or lm.get("color")==2: ai_moves+=1
        turn=g.get("turn")
        if turn in ("black",1): play_move()
    elif t=="game_over":
        p=m.get("game") or m
        print("game_over: winner=",p.get("winner"),"reason=",p.get("winReason") or p.get("reason"),flush=True); break
    elif t=="error": print("err:",m,flush=True)
print(f"RESULT: ai_moves={ai_moves} my_moves={my_moves}")
sys.exit(0 if ai_moves>=3 else 1)
