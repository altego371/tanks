// Tank Battle Game Engine
// Implements all mechanics from doc/BOT.md

const WIDTH = 20;
const HEIGHT = 20;
const MAX_TICKS = 300;
const BULLET_SPEED = 2;
const BULLET_DAMAGE = 40;
const RADIATION_START_TICK = 100;
const RADIATION_INTERVAL = 20;
const RADIATION_DAMAGE = 5;
const MEDKIT_HEAL = 50;
const MEDKIT_INTERVAL = 30;
const MEDKIT_START_TICK = 10;
const MEDKIT_MAX = 3;
const STARTING_HP = 100;
const WALL_DENSITY = 0.15;

const DIRECTIONS = {
  up: { dx: 0, dy: -1 },
  down: { dx: 0, dy: 1 },
  left: { dx: -1, dy: 0 },
  right: { dx: 1, dy: 0 },
};

function key(x, y) {
  return `${x},${y}`;
}

function generateMap() {
  const walls = new Set();

  // Borders are impassable walls
  for (let x = 0; x < WIDTH; x++) {
    walls.add(key(x, 0));
    walls.add(key(x, HEIGHT - 1));
  }
  for (let y = 0; y < HEIGHT; y++) {
    walls.add(key(0, y));
    walls.add(key(WIDTH - 1, y));
  }

  // Interior walls ~15%
  const interiorCells = [];
  for (let x = 1; x < WIDTH - 1; x++) {
    for (let y = 1; y < HEIGHT - 1; y++) {
      interiorCells.push([x, y]);
    }
  }

  const targetWalls = Math.floor(interiorCells.length * WALL_DENSITY);
  const shuffled = interiorCells.slice().sort(() => Math.random() - 0.5);

  for (let i = 0; i < targetWalls && i < shuffled.length; i++) {
    const [x, y] = shuffled[i];
    walls.add(key(x, y));
    if (!isConnected(walls)) {
      walls.delete(key(x, y));
    }
  }

  return walls;
}

function isConnected(walls) {
  // Flood fill to check all free interior cells are connected
  let start = null;
  const free = [];
  for (let x = 1; x < WIDTH - 1; x++) {
    for (let y = 1; y < HEIGHT - 1; y++) {
      if (!walls.has(key(x, y))) {
        free.push(key(x, y));
        if (!start) start = [x, y];
      }
    }
  }
  if (!start) return true;

  const visited = new Set();
  const queue = [start];
  visited.add(key(start[0], start[1]));

  while (queue.length > 0) {
    const [cx, cy] = queue.shift();
    for (const { dx, dy } of Object.values(DIRECTIONS)) {
      const nx = cx + dx;
      const ny = cy + dy;
      const k = key(nx, ny);
      if (!visited.has(k) && !walls.has(k) && nx >= 1 && nx < WIDTH - 1 && ny >= 1 && ny < HEIGHT - 1) {
        visited.add(k);
        queue.push([nx, ny]);
      }
    }
  }

  return visited.size === free.length;
}

function createGame(botConfigs) {
  const walls = generateMap();

  // Place tanks on random free cells
  const freeCells = [];
  for (let x = 1; x < WIDTH - 1; x++) {
    for (let y = 1; y < HEIGHT - 1; y++) {
      if (!walls.has(key(x, y))) {
        freeCells.push([x, y]);
      }
    }
  }

  const shuffledFree = freeCells.sort(() => Math.random() - 0.5);
  const dirs = ['up', 'down', 'left', 'right'];
  const tanks = {};

  for (let i = 0; i < botConfigs.length; i++) {
    const [x, y] = shuffledFree[i];
    tanks[botConfigs[i].id] = {
      id: botConfigs[i].id,
      x,
      y,
      direction: dirs[Math.floor(Math.random() * 4)],
      hp: STARTING_HP,
      alive: true,
      score: 0,
      kills: 0,
    };
  }

  // Convert walls set to array of [x, y] for serialization
  const wallsArray = [];
  for (const w of walls) {
    const [x, y] = w.split(',').map(Number);
    // Only include interior walls (not borders) in the walls array sent to bots
    if (x > 0 && x < WIDTH - 1 && y > 0 && y < HEIGHT - 1) {
      wallsArray.push([x, y]);
    }
  }

  return {
    tick: 0,
    width: WIDTH,
    height: HEIGHT,
    walls,
    wallsArray,
    tanks,
    bullets: [],
    pickups: [],
    radiationLevel: 0,
    over: false,
    winner: null,
    events: [],
  };
}

function getRadiationLevel(tick) {
  if (tick < RADIATION_START_TICK) return 0;
  return 1 + Math.floor((tick - RADIATION_START_TICK) / RADIATION_INTERVAL);
}

function isIrradiated(x, y, radiationLevel) {
  if (radiationLevel <= 0) return false;
  return Math.min(x - 1, y - 1, 18 - x, 18 - y) < radiationLevel;
}

function buildBotRequest(state, tankId) {
  const me = state.tanks[tankId];
  const enemies = Object.values(state.tanks).filter(t => t.id !== tankId && t.alive);

  return {
    tick: state.tick,
    me: { id: me.id, x: me.x, y: me.y, direction: me.direction, hp: me.hp, alive: me.alive },
    enemies: enemies.map(e => ({ id: e.id, x: e.x, y: e.y, direction: e.direction, hp: e.hp, alive: e.alive })),
    bullets: state.bullets.map(b => ({ x: b.x, y: b.y, direction: b.direction, owner_id: b.ownerId })),
    pickups: state.pickups.map(p => ({ x: p.x, y: p.y, kind: p.kind })),
    map: { width: state.width, height: state.height, walls: state.wallsArray },
    radiation_level: state.radiationLevel,
  };
}

const VALID_ACTIONS = new Set([
  'move_up', 'move_down', 'move_left', 'move_right',
  'rotate_up', 'rotate_down', 'rotate_left', 'rotate_right',
  'shoot', 'idle',
]);

function applyActions(state, actionMap) {
  state.events = [];

  // 1. Apply rotations
  for (const [id, action] of Object.entries(actionMap)) {
    const tank = state.tanks[id];
    if (!tank || !tank.alive) continue;
    if (!VALID_ACTIONS.has(action)) continue;

    if (action.startsWith('rotate_')) {
      const dir = action.replace('rotate_', '');
      tank.direction = dir;
    }
  }

  // 2. Apply movement
  const moves = {};
  for (const [id, action] of Object.entries(actionMap)) {
    const tank = state.tanks[id];
    if (!tank || !tank.alive) continue;

    if (action.startsWith('move_')) {
      const dir = action.replace('move_', '');
      if (tank.direction !== dir) continue; // must face direction to move

      const delta = DIRECTIONS[dir];
      const nx = tank.x + delta.dx;
      const ny = tank.y + delta.dy;

      if (state.walls.has(key(nx, ny))) continue;
      moves[id] = { x: nx, y: ny };
    }
  }

  // Resolve collisions: check for two tanks moving to same cell, or moving into another tank
  const tankPositions = {};
  for (const t of Object.values(state.tanks)) {
    if (t.alive) tankPositions[t.id] = { x: t.x, y: t.y };
  }

  // Apply moves, check conflicts
  const targetCells = {};
  for (const [id, pos] of Object.entries(moves)) {
    const k = key(pos.x, pos.y);
    if (!targetCells[k]) targetCells[k] = [];
    targetCells[k].push(id);
  }

  for (const [k, ids] of Object.entries(targetCells)) {
    if (ids.length > 1) {
      // Multiple tanks trying to move to same cell - none moves
      for (const id of ids) delete moves[id];
    }
  }

  // Check if moving into a cell occupied by a non-moving tank
  for (const [id, pos] of Object.entries(moves)) {
    const k = key(pos.x, pos.y);
    for (const t of Object.values(state.tanks)) {
      if (t.alive && t.id !== id && t.x === pos.x && t.y === pos.y && !moves[t.id]) {
        delete moves[id];
        break;
      }
    }
  }

  // Apply valid moves
  for (const [id, pos] of Object.entries(moves)) {
    state.tanks[id].x = pos.x;
    state.tanks[id].y = pos.y;
  }

  // 3. Collect medkits
  for (const tank of Object.values(state.tanks)) {
    if (!tank.alive) continue;
    const idx = state.pickups.findIndex(p => p.x === tank.x && p.y === tank.y);
    if (idx !== -1) {
      tank.hp += MEDKIT_HEAL;
      state.events.push({ type: 'medkit', tankId: tank.id, hp: tank.hp });
      state.pickups.splice(idx, 1);
    }
  }

  // 4. Spawn bullets
  for (const [id, action] of Object.entries(actionMap)) {
    const tank = state.tanks[id];
    if (!tank || !tank.alive || action !== 'shoot') continue;

    const delta = DIRECTIONS[tank.direction];
    const bx = tank.x + delta.dx;
    const by = tank.y + delta.dy;

    // If spawn cell is a wall, bullet destroyed immediately
    if (state.walls.has(key(bx, by))) continue;

    const bullet = { x: bx, y: by, direction: tank.direction, ownerId: tank.id };

    // Check if spawn cell hits a tank
    let hit = false;
    for (const t of Object.values(state.tanks)) {
      if (t.alive && t.x === bx && t.y === by) {
        applyDamage(state, t, tank, BULLET_DAMAGE);
        hit = true;
        break;
      }
    }
    if (!hit) {
      state.bullets.push(bullet);
    }
  }

  // 5. Move bullets (2 steps of 1 cell each)
  for (let step = 0; step < BULLET_SPEED; step++) {
    const remaining = [];
    for (const bullet of state.bullets) {
      const delta = DIRECTIONS[bullet.direction];
      bullet.x += delta.dx;
      bullet.y += delta.dy;

      // Check wall/border
      if (state.walls.has(key(bullet.x, bullet.y))) continue;
      if (bullet.x < 0 || bullet.x >= WIDTH || bullet.y < 0 || bullet.y >= HEIGHT) continue;

      // Check tank hit
      let hitTank = false;
      for (const t of Object.values(state.tanks)) {
        if (t.alive && t.x === bullet.x && t.y === bullet.y) {
          const owner = state.tanks[bullet.ownerId];
          applyDamage(state, t, owner, BULLET_DAMAGE);
          hitTank = true;
          break;
        }
      }
      if (!hitTank) {
        remaining.push(bullet);
      }
    }
    state.bullets = remaining;
  }

  // 6. Radiation damage
  state.radiationLevel = getRadiationLevel(state.tick);
  for (const tank of Object.values(state.tanks)) {
    if (!tank.alive) continue;
    if (isIrradiated(tank.x, tank.y, state.radiationLevel)) {
      tank.hp -= RADIATION_DAMAGE;
      if (tank.hp <= 0) {
        tank.alive = false;
        state.events.push({ type: 'radiation_kill', tankId: tank.id });
      }
    }
  }

  // 7. Spawn medkits
  if (state.tick >= MEDKIT_START_TICK && (state.tick - MEDKIT_START_TICK) % MEDKIT_INTERVAL === 0 && state.pickups.length < MEDKIT_MAX) {
    const freeCells = [];
    const tankSet = new Set(Object.values(state.tanks).filter(t => t.alive).map(t => key(t.x, t.y)));
    const pickupSet = new Set(state.pickups.map(p => key(p.x, p.y)));
    for (let x = 1; x < WIDTH - 1; x++) {
      for (let y = 1; y < HEIGHT - 1; y++) {
        const k = key(x, y);
        if (!state.walls.has(k) && !tankSet.has(k) && !pickupSet.has(k)) {
          freeCells.push([x, y]);
        }
      }
    }
    if (freeCells.length > 0) {
      const [mx, my] = freeCells[Math.floor(Math.random() * freeCells.length)];
      state.pickups.push({ x: mx, y: my, kind: 'medkit' });
    }
  }

  // 8. Check end conditions
  const alive = Object.values(state.tanks).filter(t => t.alive);
  if (alive.length <= 1 || state.tick >= MAX_TICKS - 1) {
    state.over = true;
    if (alive.length === 1) {
      const winner = alive[0];
      state.winner = winner.id;
      winner.score += winner.hp >= 100 ? 5 : 3;
    } else if (alive.length > 1) {
      // Highest HP wins
      alive.sort((a, b) => b.hp - a.hp);
      if (alive[0].hp > alive[1].hp) {
        state.winner = alive[0].id;
        alive[0].score += alive[0].hp >= 100 ? 5 : 3;
      }
      // else draw
    }
  }

  state.tick++;
  return state;
}

function applyDamage(state, target, attacker, damage) {
  target.hp -= damage;
  state.events.push({ type: 'hit', targetId: target.id, attackerId: attacker?.id, damage });
  if (target.hp <= 0) {
    target.alive = false;
    if (attacker) {
      attacker.kills++;
      attacker.score += 1;
    }
    state.events.push({ type: 'kill', targetId: target.id, attackerId: attacker?.id });
  }
}

function getPublicState(state) {
  return {
    tick: state.tick,
    width: state.width,
    height: state.height,
    walls: state.wallsArray,
    tanks: Object.values(state.tanks),
    bullets: state.bullets.map(b => ({ x: b.x, y: b.y, direction: b.direction, ownerId: b.ownerId })),
    pickups: state.pickups,
    radiationLevel: state.radiationLevel,
    over: state.over,
    winner: state.winner,
    events: state.events,
  };
}

module.exports = { createGame, buildBotRequest, applyActions, getPublicState, isIrradiated };
