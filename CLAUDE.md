# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Tank Battle tournament system — competitive bots fight on a 20x20 grid. Full game rules in `doc/BOT.md`, strategy hints in `doc/PROMPT.md`.

## Architecture

- **server/** — Node.js (Express + WebSocket) game simulation server
  - `game.js` — game engine implementing all mechanics from BOT.md
  - `index.js` — HTTP/WS server, calls bots each tick, broadcasts state to browser
  - `public/index.html` — canvas frontend for live game visualization
- **bots/** — each subdirectory is a standalone Go bot (own `go.mod`)
  - `bots/simple/` — reference bot: shoot in line-of-sight, chase closest enemy

## Commands

### Server
```bash
cd server && npm install       # first time
cd server && npm run dev       # development with hot reload (nodemon)
cd server && npm start         # production
```

### Bot (Go)
```bash
cd bots/simple && go run . -port 3001              # run directly
cd bots/simple && GOOS=linux GOARCH=amd64 go build -o bot .  # build binary for tournament
```

### Running a match
1. Start server: `cd server && npm run dev`
2. Start bot-1: `cd bots/simple && go run . -port 3001`
3. Start bot-2: `cd bots/simple && go run . -port 3002`
4. Open `http://localhost:3000`, click START GAME

## Bot protocol

Bots are HTTP servers. The game server sends `POST /action` with JSON game state every 200ms tick. Bot must respond with `{"action": "..."}` within 100ms or defaults to `idle`. See `doc/BOT.md` for full request/response schema and valid actions.

## Configuration

- Server port: `PORT` env var (default 3000)
- Bot endpoints: `BOTS` array in `server/index.js`
- Bot port: `-port` flag (default 3001)
- Tick interval: 200ms, bot timeout: 100ms (constants in `server/index.js`)

## Bot requirements

- Language: Go 1.21+, standard library only
- Must compile: `GOOS=linux GOARCH=amd64 go build -o bot .`
- Must serve `POST /action` on configurable port
