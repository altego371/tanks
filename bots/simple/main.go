package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
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

var wallSet map[[2]int]bool

func buildWallSet(walls []Wall, w, h int) {
	wallSet = make(map[[2]int]bool)
	for _, wall := range walls {
		wallSet[[2]int{wall[0], wall[1]}] = true
	}
	// Borders
	for i := 0; i < w; i++ {
		wallSet[[2]int{i, 0}] = true
		wallSet[[2]int{i, h - 1}] = true
	}
	for i := 0; i < h; i++ {
		wallSet[[2]int{0, i}] = true
		wallSet[[2]int{w - 1, i}] = true
	}
}

func isBlocked(x, y int) bool {
	return wallSet[[2]int{x, y}]
}

func dist(x1, y1, x2, y2 int) float64 {
	dx := float64(x1 - x2)
	dy := float64(y1 - y2)
	return math.Abs(dx) + math.Abs(dy)
}

func dirDelta(d Direction) (int, int) {
	switch d {
	case DirUp:
		return 0, -1
	case DirDown:
		return 0, 1
	case DirLeft:
		return -1, 0
	case DirRight:
		return 1, 0
	}
	return 0, 0
}

func dirToTarget(mx, my, tx, ty int) Direction {
	dx := tx - mx
	dy := ty - my
	if abs(dx) >= abs(dy) {
		if dx > 0 {
			return DirRight
		}
		return DirLeft
	}
	if dy > 0 {
		return DirDown
	}
	return DirUp
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func hasLineOfSight(x1, y1, x2, y2 int) bool {
	if x1 != x2 && y1 != y2 {
		return false
	}
	dx, dy := 0, 0
	if x2 > x1 {
		dx = 1
	} else if x2 < x1 {
		dx = -1
	}
	if y2 > y1 {
		dy = 1
	} else if y2 < y1 {
		dy = -1
	}
	cx, cy := x1+dx, y1+dy
	for cx != x2 || cy != y2 {
		if isBlocked(cx, cy) {
			return false
		}
		cx += dx
		cy += dy
	}
	return true
}

func shootDirection(mx, my int, enemy Tank) (Direction, bool) {
	if mx == enemy.X && my == enemy.Y {
		return "", false
	}
	if mx == enemy.X {
		if enemy.Y < my && hasLineOfSight(mx, my, enemy.X, enemy.Y) {
			return DirUp, true
		}
		if enemy.Y > my && hasLineOfSight(mx, my, enemy.X, enemy.Y) {
			return DirDown, true
		}
	}
	if my == enemy.Y {
		if enemy.X < mx && hasLineOfSight(mx, my, enemy.X, enemy.Y) {
			return DirLeft, true
		}
		if enemy.X > mx && hasLineOfSight(mx, my, enemy.X, enemy.Y) {
			return DirRight, true
		}
	}
	return "", false
}

func decide(req BotRequest) string {
	me := req.Me
	if !me.Alive {
		return "idle"
	}

	if wallSet == nil {
		buildWallSet(req.Map.Walls, req.Map.Width, req.Map.Height)
	}

	// 1. Try to shoot an enemy in line of sight
	for _, enemy := range req.Enemies {
		if !enemy.Alive {
			continue
		}
		dir, ok := shootDirection(me.X, me.Y, enemy)
		if ok {
			if me.Direction == dir {
				return "shoot"
			}
			return "rotate_" + string(dir)
		}
	}

	// 2. Move toward closest enemy
	var closest *Tank
	minDist := math.MaxFloat64
	for i := range req.Enemies {
		if !req.Enemies[i].Alive {
			continue
		}
		d := dist(me.X, me.Y, req.Enemies[i].X, req.Enemies[i].Y)
		if d < minDist {
			minDist = d
			closest = &req.Enemies[i]
		}
	}

	if closest != nil {
		dir := dirToTarget(me.X, me.Y, closest.X, closest.Y)
		if me.Direction != dir {
			return "rotate_" + string(dir)
		}
		dx, dy := dirDelta(dir)
		nx, ny := me.X+dx, me.Y+dy
		if !isBlocked(nx, ny) {
			return "move_" + string(dir)
		}
	}

	return "idle"
}

func main() {
	port := flag.Int("port", 3001, "bot server port")
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
	log.Printf("[simple-bot] listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
