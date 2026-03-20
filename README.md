# Tank Battle

Competitive bot tournament on a 20x20 grid. See [doc/BOT.md](doc/BOT.md) for full game rules and bot protocol.

## Quick Start

```bash
# 1. Install server dependencies (first time)
cd server && npm install

# 2. Start server (with hot reload)
cd server && npm run dev

# 3. Start bots (in separate terminals)
cd bots/simple && go run . -port 3001
cd bots/simple && go run . -port 3002

# 4. Open http://localhost:3000 and click START
```

## Build bot binary for tournament

```bash
cd bots/simple && GOOS=linux GOARCH=amd64 go build -o bot .
```
