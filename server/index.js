const express = require('express');
const http = require('http');
const { WebSocketServer } = require('ws');
const path = require('path');
const { createGame, buildBotRequest, applyActions, getPublicState } = require('./game');

const PORT = process.env.PORT || 3000;
let tickInterval = 200;
const BOT_TIMEOUT = 100;

// Bot configuration — edit URLs/IDs here
const BOTS = [
  { id: 'bot-1', url: 'http://localhost:3001' },
  { id: 'bot-2', url: 'http://localhost:3002' },
];

const app = express();
const server = http.createServer(app);
const wss = new WebSocketServer({ server });

app.use(express.static(path.join(__dirname, 'public')));
app.use(express.json());

const RESTART_DELAY = 1000; // ms pause between games

let gameState = null;
let tickTimer = null;
let running = false;

// Cumulative stats across all games
const stats = {
  games: 0,
  wins: {},   // botId -> win count
  draws: 0,
  kills: {},  // botId -> total kills
  history: [], // per-game results
};
for (const b of BOTS) {
  stats.wins[b.id] = 0;
  stats.kills[b.id] = 0;
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
    clearInterval(tickTimer);
    tickTimer = null;

    // Record stats
    stats.games++;
    const result = { game: stats.games, winner: gameState.winner, tick: gameState.tick, tanks: {} };
    for (const tank of Object.values(gameState.tanks)) {
      stats.kills[tank.id] = (stats.kills[tank.id] || 0) + tank.kills;
      result.tanks[tank.id] = { hp: tank.hp, kills: tank.kills, alive: tank.alive };
    }
    if (gameState.winner) {
      stats.wins[gameState.winner] = (stats.wins[gameState.winner] || 0) + 1;
    } else {
      stats.draws++;
    }
    stats.history.push(result);

    console.log(`Game #${stats.games} over at tick ${gameState.tick}. Winner: ${gameState.winner || 'draw'}`);
    broadcast({ type: 'stats', data: stats });

    // Auto-start next game after delay
    if (running) {
      setTimeout(startGame, RESTART_DELAY);
    }
  }
}

function startGame() {
  if (tickTimer) clearInterval(tickTimer);
  running = true;
  gameState = createGame(BOTS);
  console.log(`Game #${stats.games + 1} started`);
  broadcast({ type: 'state', data: getPublicState(gameState) });
  broadcast({ type: 'stats', data: stats });
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
  res.json(stats);
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
      if (data.type === 'stop') stopGames();
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
