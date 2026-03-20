# Tank Battle — Bot Developer Guide

## Overview

Your bot is an HTTP server (port 8080) written in **Go**, compiled into a **single executable binary**. The tournament server calls `POST /action` on your bot every tick (200ms). You have **100ms** to respond, otherwise the server defaults to `idle`.

**Requirements:**
- Language: **Go** (1.21+)
- No external dependencies (only standard library)
- Must compile with `go build -o bot .` into a single binary
- Must comile with `GOOS=linux GOARCH=amd64` flags

## Game Mechanics

### Map
- Grid: **20x20** cells
- Borders (row/col 0 and 19) are impassable walls
- Interior: ~15% random walls, guaranteed no enclosed areas (all free cells connected)
- Coordinate system: `(0,0)` is top-left, `x` goes right, `y` goes down

### Tank
- Starting HP: **100**
- Direction: one of `up`, `down`, `left`, `right`
- Can move 1 cell per tick in the direction it faces
- Must rotate before moving in a new direction
- Cannot move into walls, borders, or other tanks

### Bullets
- Damage: **40 HP** per hit (3 hits to kill from full HP)
- Speed: **2 cells per tick**
- Spawns 1 cell ahead of the tank in its facing direction
- Destroyed on hitting a wall, border, or tank
- A tank CAN kill an adjacent tank (bullet checks every cell including spawn)

### Radiation
- **Tick 0-99**: no radiation
- **Tick 100**: radiation appears on the outermost ring of interior cells
- **Every 20 ticks** after that: radiation grows 1 cell inward toward the center
- **5 HP per tick** for any tank standing in the radiation zone
- A cell at `(x, y)` is irradiated if `min(x-1, y-1, 18-x, 18-y) < radiation_level`

### Medkits (Pickups)
- Spawn every **30 ticks** starting from tick 10
- Max **3** on the map simultaneously
- Heal **+50 HP** (no cap -- HP can exceed 100)
- Collected by stepping on the cell (the pickup disappears)

### Match
- Max **300 ticks**
- Match ends when <=1 tank alive, or tick limit reached
- On timeout: winner = highest HP (draw if tied)

### Scoring
- **+5** -- flawless win (winner has HP >= 100)
- **+3** -- regular win
- **+1** -- per kill (attributed to bullet owner; radiation kills give no points)

## Bot Protocol

### Request: `POST /action`

The server sends a JSON body:

```json
{
  "tick": 42,
  "me": {
    "id": "team-alpha",
    "x": 5,
    "y": 3,
    "direction": "right",
    "hp": 80,
    "alive": true
  },
  "enemies": [
    {"id": "team-beta", "x": 10, "y": 3, "direction": "left", "hp": 60, "alive": true}
  ],
  "bullets": [
    {"x": 8, "y": 3, "direction": "left", "owner_id": "team-beta"}
  ],
  "pickups": [
    {"x": 7, "y": 9, "kind": "medkit"}
  ],
  "map": {
    "width": 20,
    "height": 20,
    "walls": [[3, 5], [3, 6], [7, 2]]
  },
  "radiation_level": 2
}
```

### Response

Return a JSON object with one `action` field:

```json
{"action": "shoot"}
```

### Valid Actions

| Action | Effect |
|---|---|
| `move_up` | Move 1 cell up (must be facing up) |
| `move_down` | Move 1 cell down (must be facing down) |
| `move_left` | Move 1 cell left (must be facing left) |
| `move_right` | Move 1 cell right (must be facing right) |
| `rotate_up` | Turn to face up |
| `rotate_down` | Turn to face down |
| `rotate_left` | Turn to face left |
| `rotate_right` | Turn to face right |
| `shoot` | Fire a bullet in the current direction |
| `idle` | Do nothing |

**Important**: `move_*` only works if the tank is already facing that direction. If facing right and you send `move_up`, nothing happens. You must `rotate_up` first, then `move_up` on the next tick.

## Quick Start

### Project Structure

```
my-bot/
  main.go
  go.mod
```

### go.mod

```
module my-bot

go 1.21
```

### Minimal Bot

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
)

type Direction string

const (
	DirUp    Direction = "up"
	DirDown  Direction = "down"
	DirLeft  Direction = "left"
	DirRight Direction = "right"
)

type Tank struct {
	ID        string    `json:"id"`
	X         int       `json:"x"`
	Y         int       `json:"y"`
	Direction Direction `json:"direction"`
	HP        int       `json:"hp"`
	Alive     bool      `json:"alive"`
}

type Bullet struct {
	X         int       `json:"x"`
	Y         int       `json:"y"`
	Direction Direction `json:"direction"`
	OwnerID   string    `json:"owner_id"`
}

type Pickup struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Kind string `json:"kind"`
}

type Wall [2]int

type MapState struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Walls  []Wall `json:"walls"`
}

type BotRequest struct {
	Tick           int      `json:"tick"`
	Me             Tank     `json:"me"`
	Enemies        []Tank   `json:"enemies"`
	Bullets        []Bullet `json:"bullets"`
	Pickups        []Pickup `json:"pickups"`
	Map            MapState `json:"map"`
	RadiationLevel int      `json:"radiation_level"`
}

type BotResponse struct {
	Action string `json:"action"`
}

func decide(req BotRequest) string {
	// Your strategy here!
	return "idle"
}

func main() {
	port := flag.Int("port", 8080, "bot server port")
	flag.Parse()

	http.HandleFunc("/action", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "post only", http.StatusMethodNotAllowed)
			return
		}
		var req BotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		action := decide(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BotResponse{Action: action})
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[bot] listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
```

### Build

```bash
cd my-bot
GOOS=linux GOARCH=amd64 go build -o bot .
```