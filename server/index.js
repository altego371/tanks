const express = require('express');
const http = require('http');
const { WebSocketServer } = require('ws');
const path = require('path');
const { createGame, buildBotRequest, applyActions, getPublicState } = require('./game');

const PORT = process.env.PORT || 3000;
const TICK_INTERVAL = 200;
const BOT_TIMEOUT = 100;

// Bot configuration — edit URLs/IDs here
const BOTS = [
  { id: 'bot-1', url: 'http://localhost:8081' },
  { id: 'bot-2', url: 'http://localhost:8082' },
];

const app = express();
const server = http.createServer(app);
const wss = new WebSocketServer({ server });

app.use(express.static(path.join(__dirname, 'public')));
app.use(express.json());

let gameState = null;
let tickTimer = null;

function broadcast(data) {
  const json = JSON.stringify(data);
  for (const ws of wss.clients) {
    if (ws.readyState === 1) ws.send(json);
  }
}

async function callBot(botConfig, state) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), BOT_TIMEOUT);

  try {
    const body = JSON.stringify(buildBotRequest(state, botConfig.id));
    const res = await fetch(`${botConfig.url}/action`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
      signal: controller.signal,
    });
    const data = await res.json();
    return data.action || 'idle';
  } catch {
    return 'idle';
  } finally {
    clearTimeout(timeout);
  }
}

async function tick() {
  if (!gameState || gameState.over) return;

  // Collect actions from all alive bots in parallel
  const aliveBots = BOTS.filter(b => {
    const tank = gameState.tanks[b.id];
    return tank && tank.alive;
  });

  const results = await Promise.allSettled(
    aliveBots.map(b => callBot(b, gameState))
  );

  const actionMap = {};
  aliveBots.forEach((bot, i) => {
    actionMap[bot.id] = results[i].status === 'fulfilled' ? results[i].value : 'idle';
  });

  gameState = applyActions(gameState, actionMap);
  broadcast({ type: 'state', data: getPublicState(gameState) });

  if (gameState.over) {
    console.log(`Game over at tick ${gameState.tick}. Winner: ${gameState.winner || 'draw'}`);
    clearInterval(tickTimer);
    tickTimer = null;
  }
}

function startGame() {
  if (tickTimer) clearInterval(tickTimer);
  gameState = createGame(BOTS);
  console.log(`Game started with ${BOTS.length} bots`);
  broadcast({ type: 'state', data: getPublicState(gameState) });
  tickTimer = setInterval(tick, TICK_INTERVAL);
}

// REST API
app.post('/api/start', (req, res) => {
  startGame();
  res.json({ ok: true });
});

app.get('/api/state', (req, res) => {
  res.json(gameState ? getPublicState(gameState) : null);
});

// WebSocket — send current state on connect
wss.on('connection', (ws) => {
  if (gameState) {
    ws.send(JSON.stringify({ type: 'state', data: getPublicState(gameState) }));
  }
  ws.on('message', (msg) => {
    try {
      const data = JSON.parse(msg);
      if (data.type === 'start') startGame();
    } catch {}
  });
});

server.listen(PORT, () => {
  console.log(`Tank Battle server: http://localhost:${PORT}`);
});
