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

type Point struct{ X, Y int }

// --- Helpers ---

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func manhattan(x1, y1, x2, y2 int) int { return abs(x1-x2) + abs(y1-y2) }

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

func dirFromDelta(dx, dy int) Direction {
	if dx == 1 {
		return DirRight
	}
	if dx == -1 {
		return DirLeft
	}
	if dy == 1 {
		return DirDown
	}
	return DirUp
}

var allDirs = []Direction{DirUp, DirDown, DirLeft, DirRight}

// --- Game State ---

type GameState struct {
	Req        BotRequest
	WallSet    map[Point]bool
	EnemyPos   map[Point]bool
	DangerMap  map[Point]int  // cell → earliest tick bullet arrives
	ThreatLane map[Point]bool // cells enemies are aiming at (LOS)
}

// --- Anti-oscillation + enemy tracking state (persisted across ticks) ---
var (
	prevPos      Point
	prevPrevPos  Point
	ticksSeen    int
	lastEnemyPos = make(map[string]Point)
	enemyStatic  = make(map[string]int) // how many ticks enemy hasn't moved
)

// closestMedkit returns the nearest safe medkit and its distance, or nil
func closestMedkit(me Tank, gs *GameState) (*Point, int) {
	var bestP *Point
	bestDist := math.MaxInt32
	for _, p := range gs.Req.Pickups {
		// Skip if already irradiated
		if gs.isIrradiated(p.X, p.Y) {
			continue
		}
		d := manhattan(me.X, me.Y, p.X, p.Y)
		// Skip if it will be irradiated by the time we arrive
		// (distance ≈ ticks to arrive, add small buffer)
		if gs.willBeIrradiated(p.X, p.Y, d+5) {
			continue
		}
		if d < bestDist {
			bestDist = d
			pt := Point{p.X, p.Y}
			bestP = &pt
		}
	}
	return bestP, bestDist
}

// nextMedkitTick returns how many ticks until the next medkit spawns
func nextMedkitTick(currentTick int) int {
	// Medkits spawn every 30 ticks starting from tick 10
	// Spawn ticks: 10, 40, 70, 100, 130, ...
	if currentTick < 10 {
		return 10 - currentTick
	}
	ticksSinceFirst := currentTick - 10
	ticksInCycle := ticksSinceFirst % 30
	if ticksInCycle == 0 {
		return 0 // spawning this tick
	}
	return 30 - ticksInCycle
}

// moveSafe tries to move away from all enemies (for survival retreat)
func moveSafe(me Tank, gs *GameState) string {
	type opt struct {
		action string
		canNow bool
		enemyD int
		safe   bool
	}
	var opts []opt
	for _, d := range allDirs {
		ddx, ddy := dirDelta(d)
		nx, ny := me.X+ddx, me.Y+ddy
		if gs.blocked(nx, ny) || gs.isIrradiated(nx, ny) {
			continue
		}
		np := Point{nx, ny}
		safe := !gs.ThreatLane[np]
		if _, bd := gs.DangerMap[np]; bd {
			safe = false
		}
		// distance to nearest enemy from this new cell
		minED := math.MaxInt32
		for _, e := range gs.Req.Enemies {
			if e.Alive {
				ed := manhattan(nx, ny, e.X, e.Y)
				if ed < minED {
					minED = ed
				}
			}
		}
		canNow := me.Direction == d
		if canNow {
			opts = append(opts, opt{"move_" + string(d), true, minED, safe})
		} else {
			opts = append(opts, opt{"rotate_" + string(d), false, minED, safe})
		}
	}
	// prefer: safe + canNow + maximizes enemy distance
	best := ""
	bestScore := -1
	for _, o := range opts {
		score := o.enemyD
		if o.safe {
			score += 100
		}
		if o.canNow {
			score += 50
		}
		if score > bestScore {
			bestScore = score
			best = o.action
		}
	}
	return best
}

func newGameState(req BotRequest) *GameState {
	gs := &GameState{Req: req}
	gs.WallSet = make(map[Point]bool, len(req.Map.Walls)+80)
	for _, w := range req.Map.Walls {
		gs.WallSet[Point{w[0], w[1]}] = true
	}
	for i := 0; i < req.Map.Width; i++ {
		gs.WallSet[Point{i, 0}] = true
		gs.WallSet[Point{i, req.Map.Height - 1}] = true
	}
	for i := 0; i < req.Map.Height; i++ {
		gs.WallSet[Point{0, i}] = true
		gs.WallSet[Point{req.Map.Width - 1, i}] = true
	}
	gs.EnemyPos = make(map[Point]bool)
	for _, e := range req.Enemies {
		if e.Alive {
			gs.EnemyPos[Point{e.X, e.Y}] = true
		}
	}
	gs.DangerMap = gs.buildDangerMap(4)
	gs.ThreatLane = gs.buildThreatLanes()
	return gs
}

func (gs *GameState) isWall(x, y int) bool { return gs.WallSet[Point{x, y}] }

func (gs *GameState) blocked(x, y int) bool {
	if x < 0 || y < 0 || x >= gs.Req.Map.Width || y >= gs.Req.Map.Height {
		return true
	}
	p := Point{x, y}
	return gs.WallSet[p] || gs.EnemyPos[p]
}

func (gs *GameState) isIrradiated(x, y int) bool {
	rl := gs.Req.RadiationLevel
	if rl <= 0 {
		return false
	}
	return minI(minI(x-1, y-1), minI(18-x, 18-y)) < rl
}

func (gs *GameState) willBeIrradiated(x, y, ticksAhead int) bool {
	ft := gs.Req.Tick + ticksAhead
	if ft < 100 {
		return false
	}
	rl := 1 + (ft-100)/20
	return minI(minI(x-1, y-1), minI(18-x, 18-y)) < rl
}

// --- FIX #2: Build threat lanes — cells that live enemies are aiming at ---

func (gs *GameState) buildThreatLanes() map[Point]bool {
	lanes := make(map[Point]bool)
	for _, e := range gs.Req.Enemies {
		if !e.Alive {
			continue
		}
		dx, dy := dirDelta(e.Direction)
		x, y := e.X+dx, e.Y+dy
		for x > 0 && x < gs.Req.Map.Width-1 && y > 0 && y < gs.Req.Map.Height-1 {
			if gs.isWall(x, y) {
				break
			}
			lanes[Point{x, y}] = true
			x += dx
			y += dy
		}
	}
	return lanes
}

// --- Bullet danger map ---

func (gs *GameState) buildDangerMap(maxTicks int) map[Point]int {
	danger := make(map[Point]int)
	for _, b := range gs.Req.Bullets {
		dx, dy := dirDelta(b.Direction)
		pos := 0
		for t := 1; t <= maxTicks; t++ {
			for step := 1; step <= 2; step++ {
				pos++
				nx := b.X + dx*pos
				ny := b.Y + dy*pos
				if gs.isWall(nx, ny) {
					goto nextBullet
				}
				if _, ok := danger[Point{nx, ny}]; !ok {
					danger[Point{nx, ny}] = t
				}
			}
		}
	nextBullet:
	}
	return danger
}

// --- FIX #3: Enemy aim threat detection ---
// Returns true if ANY live enemy has clear LOS to cell (tx,ty) along their facing direction

func (gs *GameState) enemyAimedAt(tx, ty int) bool {
	return gs.ThreatLane[Point{tx, ty}]
}

// Returns the enemy that is aiming at us, and the distance
func (gs *GameState) enemyAimingAtMe(me Tank) (*Tank, int) {
	for i := range gs.Req.Enemies {
		e := &gs.Req.Enemies[i]
		if !e.Alive {
			continue
		}
		dx, dy := dirDelta(e.Direction)
		x, y := e.X+dx, e.Y+dy
		for x > 0 && x < gs.Req.Map.Width-1 && y > 0 && y < gs.Req.Map.Height-1 {
			if gs.isWall(x, y) {
				break
			}
			if x == me.X && y == me.Y {
				return e, manhattan(e.X, e.Y, me.X, me.Y)
			}
			x += dx
			y += dy
		}
	}
	return nil, 0
}

// --- Line of sight for shooting ---

func (gs *GameState) losEnemy(me Tank) *Tank {
	dx, dy := dirDelta(me.Direction)
	x, y := me.X+dx, me.Y+dy
	for x > 0 && x < gs.Req.Map.Width-1 && y > 0 && y < gs.Req.Map.Height-1 {
		if gs.isWall(x, y) {
			return nil
		}
		for i := range gs.Req.Enemies {
			e := &gs.Req.Enemies[i]
			if e.Alive && e.X == x && e.Y == y {
				return e
			}
		}
		x += dx
		y += dy
	}
	return nil
}

func (gs *GameState) losAfterRotate(me Tank, dir Direction) (*Tank, int) {
	dx, dy := dirDelta(dir)
	x, y := me.X+dx, me.Y+dy
	for x > 0 && x < gs.Req.Map.Width-1 && y > 0 && y < gs.Req.Map.Height-1 {
		if gs.isWall(x, y) {
			return nil, 0
		}
		for i := range gs.Req.Enemies {
			e := &gs.Req.Enemies[i]
			if e.Alive && e.X == x && e.Y == y {
				return e, manhattan(me.X, me.Y, e.X, e.Y)
			}
		}
		x += dx
		y += dy
	}
	return nil, 0
}

// --- Dodge logic ---

func (gs *GameState) findDodgeAction(me Tank) string {
	myP := Point{me.X, me.Y}

	// Check bullet danger
	bulletTick, bulletDanger := gs.DangerMap[myP]
	if !bulletDanger || bulletTick > 2 {
		return ""
	}

	type opt struct {
		action string
		dir    Direction
		safe   bool
		canNow bool
	}

	var opts []opt
	for _, d := range allDirs {
		ddx, ddy := dirDelta(d)
		nx, ny := me.X+ddx, me.Y+ddy
		if gs.blocked(nx, ny) {
			continue
		}
		destP := Point{nx, ny}
		destBT, destDanger := gs.DangerMap[destP]
		safe := !destDanger || destBT > 2
		// FIX: also check we're not dodging INTO an enemy's firing lane
		if gs.ThreatLane[destP] {
			safe = false
		}
		canNow := me.Direction == d
		if canNow {
			opts = append(opts, opt{"move_" + string(d), d, safe, true})
		} else {
			opts = append(opts, opt{"rotate_" + string(d), d, safe, false})
		}
	}

	// Prefer: safe+now > safe+rotate > now > rotate
	for _, o := range opts {
		if o.safe && o.canNow {
			return o.action
		}
	}
	for _, o := range opts {
		if o.safe {
			return o.action
		}
	}
	for _, o := range opts {
		if o.canNow {
			return o.action
		}
	}
	if len(opts) > 0 {
		return opts[0].action
	}
	return ""
}

// --- FIX #3: Preemptive dodge when enemy aims at us (before bullet exists) ---

func (gs *GameState) preemptiveDodge(me Tank) string {
	aimer, dist := gs.enemyAimingAtMe(me)
	if aimer == nil {
		return ""
	}

	// If we can shoot them back RIGHT NOW (they're in our LOS), shoot instead
	if target := gs.losEnemy(me); target != nil && target.ID == aimer.ID {
		return "shoot"
	}

	// If enemy is close enough to hit us next tick, dodge is critical
	// Bullet travels 3 cells on first tick (spawn+2), so within 3 cells = instant danger
	if dist > 5 {
		// Far away — we have time to rotate and shoot instead of dodge
		// Try to rotate to face them
		for _, d := range allDirs {
			if e, _ := gs.losAfterRotate(me, d); e != nil && e.ID == aimer.ID {
				return "rotate_" + string(d)
			}
		}
	}

	// Need to dodge off-axis. Move perpendicular to the threat direction.
	type opt struct {
		action string
		canNow bool
		safe   bool
	}
	var opts []opt
	for _, d := range allDirs {
		ddx, ddy := dirDelta(d)
		nx, ny := me.X+ddx, me.Y+ddy
		if gs.blocked(nx, ny) {
			continue
		}
		np := Point{nx, ny}
		safe := !gs.ThreatLane[np]
		if _, bd := gs.DangerMap[np]; bd {
			safe = false
		}
		canNow := me.Direction == d
		if canNow {
			opts = append(opts, opt{"move_" + string(d), true, safe})
		} else {
			opts = append(opts, opt{"rotate_" + string(d), false, safe})
		}
	}

	for _, o := range opts {
		if o.safe && o.canNow {
			return o.action
		}
	}
	for _, o := range opts {
		if o.safe {
			return o.action
		}
	}
	for _, o := range opts {
		if o.canNow {
			return o.action
		}
	}
	return ""
}

// --- BFS with lane avoidance ---

func (gs *GameState) bfsPath(sx, sy, gx, gy int, avoidLanes bool) []Point {
	if sx == gx && sy == gy {
		return []Point{}
	}
	type nd struct {
		p    Point
		dist int
	}
	visited := make(map[Point]Point)
	start := Point{sx, sy}
	goal := Point{gx, gy}
	visited[start] = Point{-1, -1}
	queue := []nd{{start, 0}}
	dirs := []Point{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.p == goal {
			path := []Point{}
			c := goal
			for c != start {
				path = append([]Point{c}, path...)
				c = visited[c]
			}
			return path
		}
		for _, d := range dirs {
			nx, ny := cur.p.X+d.X, cur.p.Y+d.Y
			np := Point{nx, ny}
			if _, seen := visited[np]; seen {
				continue
			}
			isGoal := nx == gx && ny == gy
			if !isGoal && gs.blocked(nx, ny) {
				continue
			}
			if isGoal && gs.isWall(nx, ny) {
				continue
			}
			arrivalTick := cur.dist + 1
			// Avoid bullet paths
			if bulletT, ok := gs.DangerMap[np]; ok {
				if abs(bulletT-arrivalTick) <= 1 {
					continue
				}
			}
			// FIX #2: Avoid enemy firing lanes during pathfinding (not for final goal)
			if avoidLanes && !isGoal && gs.ThreatLane[np] {
				continue
			}
			visited[np] = cur.p
			queue = append(queue, nd{np, arrivalTick})
		}
	}
	return nil
}

func moveToward(me Tank, target Point, gs *GameState, avoidLanes bool) string {
	path := gs.bfsPath(me.X, me.Y, target.X, target.Y, avoidLanes)
	if path == nil || len(path) == 0 {
		return ""
	}
	next := path[0]
	needed := dirFromDelta(next.X-me.X, next.Y-me.Y)
	if me.Direction != needed {
		return "rotate_" + string(needed)
	}
	return "move_" + string(needed)
}

// --- Approach: get on same axis as enemy at safe shooting distance ---

func bestApproach(me Tank, enemy Tank, gs *GameState) Point {
	best := Point{enemy.X, enemy.Y}
	bestScore := math.MaxFloat64

	// Try positions on same row or column as enemy, 2-6 cells away
	for _, off := range []int{-3, 3, -2, 2, -4, 4, -5, 5, -6, 6} {
		candidates := []Point{
			{enemy.X + off, enemy.Y}, // same row
			{enemy.X, enemy.Y + off}, // same col
		}
		for _, c := range candidates {
			if c.X <= 0 || c.X >= 19 || c.Y <= 0 || c.Y >= 19 {
				continue
			}
			if gs.isWall(c.X, c.Y) || gs.isIrradiated(c.X, c.Y) {
				continue
			}
			score := float64(manhattan(me.X, me.Y, c.X, c.Y))
			// Penalize cells in enemy threat lanes
			if gs.ThreatLane[c] {
				score += 10
			}
			// Bonus: already aligned
			if c.X == me.X || c.Y == me.Y {
				score -= 1
			}
			if score < bestScore {
				bestScore = score
				best = c
			}
		}
	}
	return best
}

// --- Main decision engine ---

func decide(req BotRequest) string {
	gs := newGameState(req)
	me := req.Me
	if !me.Alive {
		return "idle"
	}

	// --- Track enemy movement (detect AFK/stuck bots) ---
	for _, e := range req.Enemies {
		if !e.Alive {
			continue
		}
		ep := Point{e.X, e.Y}
		if last, ok := lastEnemyPos[e.ID]; ok && last == ep {
			enemyStatic[e.ID]++
		} else {
			enemyStatic[e.ID] = 0
		}
		lastEnemyPos[e.ID] = ep
	}

	// --- Anti-oscillation: detect if we're bouncing between two cells ---
	myPos := Point{me.X, me.Y}
	oscillating := ticksSeen >= 2 && myPos == prevPrevPos && myPos != prevPos
	prevPrevPos = prevPos
	prevPos = myPos
	ticksSeen++

	// =================================================================
	// P1: DODGE active bullets (highest priority survival)
	// =================================================================
	dodgeAction := gs.findDodgeAction(me)
	if dodgeAction != "" {
		// Trade shots if we have HP advantage and can shoot right now
		if target := gs.losEnemy(me); target != nil {
			if me.HP > target.HP || me.HP > 40 {
				return "shoot"
			}
		}
		return dodgeAction
	}

	// =================================================================
	// P2: SHOOT if enemy in line of sight (always shoot when possible!)
	// =================================================================
	if target := gs.losEnemy(me); target != nil {
		return "shoot"
	}

	// =================================================================
	// P3: PREEMPTIVE DODGE — enemy aiming at us (no bullet yet)
	// FIX for "walked into theta's firing lane and didn't dodge"
	// =================================================================
	if action := gs.preemptiveDodge(me); action != "" {
		return action
	}

	// =================================================================
	// P4: Rotate to shoot nearby enemy on axis (FIX: no range cap)
	// =================================================================
	{
		bestDir := Direction("")
		bestDist := math.MaxInt32
		for _, d := range allDirs {
			if d == me.Direction {
				continue
			}
			if e, dist := gs.losAfterRotate(me, d); e != nil {
				if dist < bestDist {
					bestDist = dist
					bestDir = d
				}
			}
		}
		if bestDir != "" {
			// Only rotate if standing still is safe
			myP := Point{me.X, me.Y}
			_, bulletDanger := gs.DangerMap[myP]
			if !bulletDanger && !gs.ThreatLane[myP] {
				return "rotate_" + string(bestDir)
			}
			// Even if in threat lane, if very close and HP advantage, rotate anyway
			if !bulletDanger && bestDist <= 4 {
				return "rotate_" + string(bestDir)
			}
		}
	}

	// =================================================================
	// P5: Flee radiation
	// =================================================================
	if gs.isIrradiated(me.X, me.Y) {
		cx, cy := 10, 10
		if a := moveToward(me, Point{cx, cy}, gs, true); a != "" {
			return a
		}
		if a := moveToward(me, Point{cx, cy}, gs, false); a != "" {
			return a
		}
	}

	// =================================================================
	// P5b: HUNT STATIC/AFK ENEMIES — free kills, highest value target
	// If enemy hasn't moved for 5+ ticks, rush them aggressively
	// =================================================================
	{
		var staticTarget *Tank
		bestDist := math.MaxInt32
		for i := range req.Enemies {
			e := &req.Enemies[i]
			if !e.Alive {
				continue
			}
			if enemyStatic[e.ID] >= 5 {
				d := manhattan(me.X, me.Y, e.X, e.Y)
				if d < bestDist {
					bestDist = d
					staticTarget = e
				}
			}
		}
		if staticTarget != nil {
			// Rush directly to adjacent cell for guaranteed kill
			target := Point{staticTarget.X, staticTarget.Y}
			if a := moveToward(me, target, gs, true); a != "" {
				return a
			}
			if a := moveToward(me, target, gs, false); a != "" {
				return a
			}
		}
	}

	// =================================================================
	// P6: Collect medkits (FIX: much higher priority when hurt)
	// =================================================================
	{
		bestP := Point{-1, -1}
		bestScore := math.MaxFloat64
		for _, p := range req.Pickups {
			if gs.isIrradiated(p.X, p.Y) {
				continue
			}
			d := manhattan(me.X, me.Y, p.X, p.Y)
			score := float64(d)
			// Always prioritize medkits when hurt
			if me.HP <= 60 {
				score *= 0.3
			} else if me.HP <= 80 {
				score *= 0.6
			}
			// Penalize if enemy is closer
			for _, e := range req.Enemies {
				if e.Alive && manhattan(e.X, e.Y, p.X, p.Y) < d {
					score += 8
				}
			}
			if score < bestScore {
				bestScore = score
				bestP = Point{p.X, p.Y}
			}
		}
		// Go for medkit if hurt OR if it's very close
		if bestP.X >= 0 && (me.HP <= 80 || bestScore < 6) {
			if a := moveToward(me, bestP, gs, true); a != "" {
				return a
			}
			if a := moveToward(me, bestP, gs, false); a != "" {
				return a
			}
		}
	}

	// =================================================================
	// P7: Pre-position for future radiation
	// =================================================================
	if gs.willBeIrradiated(me.X, me.Y, 25) && !gs.isIrradiated(me.X, me.Y) {
		if a := moveToward(me, Point{10, 10}, gs, true); a != "" {
			return a
		}
	}

	// =================================================================
	// P8: Hunt closest enemy — approach on axis for shooting opportunity
	// =================================================================
	{
		var target *Tank
		bestDist := math.MaxInt32
		for i := range req.Enemies {
			e := &req.Enemies[i]
			if !e.Alive {
				continue
			}
			d := manhattan(me.X, me.Y, e.X, e.Y)
			// Prefer low HP targets (easier kills)
			if e.HP < 60 {
				d -= 3
			}
			// Strongly prefer static/AFK targets (guaranteed kills)
			if enemyStatic[e.ID] >= 3 {
				d -= 8
			}
			if d < bestDist {
				bestDist = d
				target = e
			}
		}
		if target != nil {
			pt := bestApproach(me, *target, gs)
			if a := moveToward(me, pt, gs, true); a != "" {
				return a
			}
			if a := moveToward(me, pt, gs, false); a != "" {
				return a
			}
			// Direct approach fallback
			if a := moveToward(me, Point{target.X, target.Y}, gs, false); a != "" {
				return a
			}
		}
	}

	// =================================================================
	// P9: Move toward center
	// =================================================================
	if me.X != 10 || me.Y != 10 {
		if a := moveToward(me, Point{10, 10}, gs, true); a != "" {
			return a
		}
	}

	// =================================================================
	// P10: Anti-oscillation — if stuck bouncing, try shooting or moving differently
	// =================================================================
	if oscillating {
		// If we can shoot in any direction, do it (might suppress enemies)
		if target := gs.losEnemy(me); target != nil {
			return "shoot"
		}
		// Try moving in our current facing direction if possible
		dx, dy := dirDelta(me.Direction)
		nx, ny := me.X+dx, me.Y+dy
		if !gs.blocked(nx, ny) {
			return "move_" + string(me.Direction)
		}
		// Rotate to any open direction we haven't tried
		for _, d := range allDirs {
			if d == me.Direction {
				continue
			}
			ddx, ddy := dirDelta(d)
			if !gs.blocked(me.X+ddx, me.Y+ddy) {
				return "rotate_" + string(d)
			}
		}
	}

	return "idle"
}

// --- HTTP Server ---

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
