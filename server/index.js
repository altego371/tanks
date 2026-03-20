const express = require('express');
const http = require('http');
const { WebSocketServer } = require('ws');
const path = require('path');
const { createGame, buildBotRequest, applyActions, getPublicState } = require('./game');

const PORT = process.env.PORT || 3000;
let tickInterval = 20;
const BOT_TIMEOUT = 100;

// Bot configuration — default 4 bots on ports 3001-3004
let BOTS = [
  { id: 'bot-1', url: 'http://localhost:3001' },
  { id: 'bot-2', url: 'http://localhost:3002' },
  { id: 'bot-3', url: 'http://localhost:3003' },
  { id: 'bot-4', url: 'http://localhost:3004' },
];

const app = express();
const server = http.createServer(app);
const wss = new WebSocketServer({ server });

app.use(express.static(path.join(__dirname, 'public')));
app.use(express.json());


let gameState = null;
let tickTimer = null;
let running = false;
let maxTimeoutTicks = 1; // kill bot after this many consecutive timeouts
const botTimeouts = {};  // botId -> consecutive timeout count
let pierceWalls = false; // bullets pass through single-thickness walls

let history = []; // per-game results

function computeStats() {
  const recent = history;
  const s = { games: recent.length, wins: {}, draws: 0, kills: {} };
  for (const b of BOTS) { s.wins[b.id] = 0; s.kills[b.id] = 0; }
  for (const r of recent) {
    if (r.winner) {
      s.wins[r.winner] = (s.wins[r.winner] || 0) + 1;
    } else {
      s.draws++;
    }
    for (const [id, t] of Object.entries(r.tanks)) {
      s.kills[id] = (s.kills[id] || 0) + t.kills;
    }
  }
  return s;
}

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
    return { action: data.action || 'idle', timedOut: false };
  } catch {
    return { action: 'idle', timedOut: true };
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
    const result = results[i].status === 'fulfilled' ? results[i].value : { action: 'idle', timedOut: true };
    actionMap[bot.id] = result.action;

    // Track consecutive timeouts
    if (result.timedOut) {
      botTimeouts[bot.id] = (botTimeouts[bot.id] || 0) + 1;
      if (maxTimeoutTicks > 0 && botTimeouts[bot.id] >= maxTimeoutTicks) {
        // Kill the bot
        const tank = gameState.tanks[bot.id];
        if (tank && tank.alive) {
          tank.alive = false;
          tank.hp = 0;
          gameState.events = gameState.events || [];
          gameState.events.push({ type: 'timeout_kill', tankId: bot.id, ticks: botTimeouts[bot.id] });
          console.log(`${bot.id} killed: no response for ${botTimeouts[bot.id]} ticks`);
        }
      }
    } else {
      botTimeouts[bot.id] = 0;
    }
  });

  gameState = applyActions(gameState, actionMap, { pierceWalls });
  broadcast({ type: 'state', data: getPublicState(gameState) });

  if (gameState.over) {
    clearInterval(tickTimer);
    tickTimer = null;

    // Record result
    const result = { game: history.length + 1, winner: gameState.winner, tick: gameState.tick, tanks: {} };
    for (const tank of Object.values(gameState.tanks)) {
      result.tanks[tank.id] = { hp: tank.hp, kills: tank.kills, alive: tank.alive };
    }
    history.push(result);

    console.log(`Game #${history.length} over at tick ${gameState.tick}. Winner: ${gameState.winner || 'draw'}`);
    broadcast({ type: 'stats', data: computeStats() });

    // Auto-start next game after delay
    if (running) {
      startGame();
    }
  }
}

function startGame() {
  if (tickTimer) clearInterval(tickTimer);
  running = true;
  for (const b of BOTS) botTimeouts[b.id] = 0;
  gameState = createGame(BOTS);
  console.log(`Game #${history.length + 1} started`);
  broadcast({ type: 'state', data: getPublicState(gameState) });
  broadcast({ type: 'stats', data: computeStats() });
  tickTimer = setInterval(tick, tickInterval);
}

function stopGames() {
  running = false;
  if (tickTimer) {
    clearInterval(tickTimer);
    tickTimer = null;
  }
}

// REST API
app.post('/api/start', (req, res) => {
  startGame();
  res.json({ ok: true });
});

app.post('/api/stop', (req, res) => {
  stopGames();
  res.json({ ok: true });
});

app.get('/api/state', (req, res) => {
  res.json(gameState ? getPublicState(gameState) : null);
});

app.get('/api/stats', (req, res) => {
  res.json(computeStats());
});

// WebSocket — send current state on connect
wss.on('connection', (ws) => {
  ws.send(JSON.stringify({ type: 'bots', data: BOTS }));
  ws.send(JSON.stringify({ type: 'stats', data: computeStats() }));
  if (gameState) {
    ws.send(JSON.stringify({ type: 'state', data: getPublicState(gameState) }));
  }
  ws.on('message', (msg) => {
    try {
      const data = JSON.parse(msg);
      if (data.type === 'start') startGame();
      if (data.type === 'stop') stopGames();
      if (data.type === 'reset_stats') {
        history = [];
        broadcast({ type: 'stats', data: computeStats() });
      }
      if (data.type === 'set_bots' && Array.isArray(data.bots)) {
        BOTS = data.bots.filter(b => b.id && b.url);
        history = [];
        broadcast({ type: 'bots', data: BOTS });
        broadcast({ type: 'stats', data: computeStats() });
      }
      if (data.type === 'set_pierce_walls') {
        pierceWalls = !!data.value;
      }
      if (data.type === 'set_timeout_ticks' && typeof data.value === 'number' && data.value >= 0) {
        maxTimeoutTicks = data.value;
      }
      if (data.type === 'set_tick' && typeof data.value === 'number' && data.value >= 10) {
        tickInterval = data.value;
        // Restart interval if game is running
        if (tickTimer) {
          clearInterval(tickTimer);
          tickTimer = setInterval(tick, tickInterval);
        }
      }
    } catch {}
  });
});

server.listen(PORT, () => {
  console.log(`Tank Battle server: http://localhost:${PORT}`);
});
