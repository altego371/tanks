package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
)

// ───────────────────────────── Types ─────────────────────────────

type Direction string

const (
	Up    Direction = "up"
	Down  Direction = "down"
	Left  Direction = "left"
	Right Direction = "right"
)

var allDirs = []Direction{Up, Down, Left, Right}

type Tank struct {
	ID  string    `json:"id"`
	X   int       `json:"x"`
	Y   int       `json:"y"`
	Dir Direction `json:"direction"`
	HP  int       `json:"hp"`
	Ok  bool      `json:"alive"`
}

type Bullet struct {
	X   int       `json:"x"`
	Y   int       `json:"y"`
	Dir Direction `json:"direction"`
	Own string    `json:"owner_id"`
}

type Pickup struct {
	X int    `json:"x"`
	Y int    `json:"y"`
	K string `json:"kind"`
}

type MapState struct {
	W     int      `json:"width"`
	H     int      `json:"height"`
	Walls [][2]int `json:"walls"`
}

type Req struct {
	Tick    int      `json:"tick"`
	Me      Tank     `json:"me"`
	Enemies []Tank   `json:"enemies"`
	Bullets []Bullet `json:"bullets"`
	Pickups []Pickup `json:"pickups"`
	Map     MapState `json:"map"`
	Rad     int      `json:"radiation_level"`
}

type Resp struct {
	Action string `json:"action"`
}

type Pt struct{ X, Y int }

// ───────────────────────────── Helpers ─────────────────────────────

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
func mn(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func dist(a, b Pt) int { return abs(a.X-b.X) + abs(a.Y-b.Y) }

func dd(d Direction) (int, int) {
	switch d {
	case Up:
		return 0, -1
	case Down:
		return 0, 1
	case Left:
		return -1, 0
	case Right:
		return 1, 0
	}
	return 0, 0
}

func opposite(d Direction) Direction {
	switch d {
	case Up:
		return Down
	case Down:
		return Up
	case Left:
		return Right
	case Right:
		return Left
	}
	return d
}

func dirTo(from, to Pt) Direction {
	dx, dy := to.X-from.X, to.Y-from.Y
	if dx > 0 {
		return Right
	}
	if dx < 0 {
		return Left
	}
	if dy > 0 {
		return Down
	}
	return Up
}

// ───────────────────────────── Persistent state ─────────────────────────────

var (
	prevPos, prevPrevPos Pt
	tickN                int
	lastEPos             = map[string]Pt{}
	eStatic              = map[string]int{}
)

// ───────────────────────────── Game world ─────────────────────────────

type World struct {
	R        Req
	Wall     map[Pt]bool
	Epos     map[Pt]*Tank
	Danger   map[Pt]int  // cell → earliest bullet arrival tick (1-based)
	FireLane map[Pt]bool // cells any live enemy is aiming at
	CX, CY   int         // map center
}

func newWorld(r Req) *World {
	w := &World{R: r, CX: r.Map.W / 2, CY: r.Map.H / 2}

	// walls + borders
	w.Wall = make(map[Pt]bool, len(r.Map.Walls)+80)
	for _, v := range r.Map.Walls {
		w.Wall[Pt{v[0], v[1]}] = true
	}
	for i := 0; i < r.Map.W; i++ {
		w.Wall[Pt{i, 0}] = true
		w.Wall[Pt{i, r.Map.H - 1}] = true
	}
	for i := 0; i < r.Map.H; i++ {
		w.Wall[Pt{0, i}] = true
		w.Wall[Pt{r.Map.W - 1, i}] = true
	}

	// enemy positions
	w.Epos = make(map[Pt]*Tank)
	for i := range r.Enemies {
		e := &r.Enemies[i]
		if e.Ok {
			w.Epos[Pt{e.X, e.Y}] = e
		}
	}

	// bullet danger map — simulate each bullet 4 ticks ahead
	w.Danger = make(map[Pt]int)
	for _, b := range r.Bullets {
		dx, dy := dd(b.Dir)
		pos := 0
		for t := 1; t <= 4; t++ {
			for s := 1; s <= 2; s++ {
				pos++
				nx, ny := b.X+dx*pos, b.Y+dy*pos
				if w.Wall[Pt{nx, ny}] {
					goto nextB
				}
				if _, ok := w.Danger[Pt{nx, ny}]; !ok {
					w.Danger[Pt{nx, ny}] = t
				}
			}
		}
	nextB:
	}

	// fire lanes — every cell each live enemy can shoot at
	w.FireLane = make(map[Pt]bool)
	for _, e := range r.Enemies {
		if !e.Ok {
			continue
		}
		dx, dy := dd(e.Dir)
		x, y := e.X+dx, e.Y+dy
		for x > 0 && x < r.Map.W-1 && y > 0 && y < r.Map.H-1 {
			if w.Wall[Pt{x, y}] {
				break
			}
			w.FireLane[Pt{x, y}] = true
			x += dx
			y += dy
		}
	}

	return w
}

func (w *World) blocked(x, y int) bool {
	p := Pt{x, y}
	return w.Wall[p] || w.Epos[p] != nil
}

func (w *World) irrad(x, y int) bool {
	if w.R.Rad <= 0 {
		return false
	}
	return mn(mn(x-1, y-1), mn(18-x, 18-y)) < w.R.Rad
}

func (w *World) futureIrrad(x, y, ticksAhead int) bool {
	ft := w.R.Tick + ticksAhead
	if ft < 100 {
		return false
	}
	rl := 1 + (ft-100)/20
	return mn(mn(x-1, y-1), mn(18-x, 18-y)) < rl
}

// cellDanger returns a score for how dangerous a cell is. 0 = safe.
func (w *World) cellDanger(x, y int) int {
	d := 0
	if _, ok := w.Danger[Pt{x, y}]; ok {
		d += 100
	}
	if w.FireLane[Pt{x, y}] {
		d += 50
	}
	if w.irrad(x, y) {
		d += 200
	}
	return d
}

// ───────────────────────────── BFS pathfinding ─────────────────────────────

func (w *World) bfs(sx, sy, gx, gy int, avoidDanger bool) []Pt {
	if sx == gx && sy == gy {
		return []Pt{}
	}
	start := Pt{sx, sy}
	goal := Pt{gx, gy}
	vis := map[Pt]Pt{start: {-1, -1}}
	type nd struct {
		p Pt
		d int
	}
	q := []nd{{start, 0}}
	steps := []Pt{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}

	for len(q) > 0 {
		c := q[0]
		q = q[1:]
		if c.p == goal {
			path := []Pt{}
			at := goal
			for at != start {
				path = append([]Pt{at}, path...)
				at = vis[at]
			}
			return path
		}
		for _, s := range steps {
			nx, ny := c.p.X+s.X, c.p.Y+s.Y
			np := Pt{nx, ny}
			if _, seen := vis[np]; seen {
				continue
			}
			isGoal := np == goal
			if !isGoal && w.blocked(nx, ny) {
				continue
			}
			if isGoal && w.Wall[np] {
				continue
			}
			arr := c.d + 1
			if avoidDanger && !isGoal {
				// avoid bullet paths near arrival
				if bt, ok := w.Danger[np]; ok && abs(bt-arr) <= 1 {
					continue
				}
				// avoid fire lanes
				if w.FireLane[np] {
					continue
				}
				// avoid current radiation
				if w.irrad(nx, ny) {
					continue
				}
				// avoid future radiation (based on arrival tick)
				if w.futureIrrad(nx, ny, arr+10) {
					continue
				}
			}
			vis[np] = c.p
			q = append(q, nd{np, arr})
		}
	}
	return nil
}

func (w *World) moveTo(me Tank, tgt Pt) string {
	// try safe path first, then unsafe fallback
	path := w.bfs(me.X, me.Y, tgt.X, tgt.Y, true)
	if path == nil {
		path = w.bfs(me.X, me.Y, tgt.X, tgt.Y, false)
	}
	if path == nil || len(path) == 0 {
		return ""
	}
	next := path[0]
	need := dirTo(Pt{me.X, me.Y}, next)
	if me.Dir != need {
		return "rotate_" + string(need)
	}
	return "move_" + string(need)
}

// ───────────────────────────── Shooting ─────────────────────────────

// clearLOS: is there a live enemy in our direct line of fire with no walls?
func (w *World) clearLOS(me Tank) *Tank {
	dx, dy := dd(me.Dir)
	x, y := me.X+dx, me.Y+dy
	for x > 0 && x < w.R.Map.W-1 && y > 0 && y < w.R.Map.H-1 {
		if w.Wall[Pt{x, y}] {
			return nil
		}
		if e := w.Epos[Pt{x, y}]; e != nil {
			return e
		}
		x += dx
		y += dy
	}
	return nil
}

// aggroLOS: enemy on our axis, shoot through walls ONLY if:
// 1. wall is directly adjacent (no gap between tank and wall)
// 2. wall is exactly ONE cell thick (cell behind it is open)
// 3. there is only ONE wall between us and the enemy (no more walls after it)
func (w *World) aggroLOS(me Tank) *Tank {
	dx, dy := dd(me.Dir)
	fx, fy := me.X+dx, me.Y+dy
	// first cell must be a wall
	if !w.Wall[Pt{fx, fy}] {
		return nil
	}
	// cell behind the wall must NOT be a wall (single-thick)
	bx, by := fx+dx, fy+dy
	if w.Wall[Pt{bx, by}] {
		return nil
	}
	// trace from behind the wall — enemy must appear before any other wall
	x, y := bx, by
	for x > 0 && x < w.R.Map.W-1 && y > 0 && y < w.R.Map.H-1 {
		if w.Wall[Pt{x, y}] {
			return nil // second wall before enemy — don't shoot
		}
		if e := w.Epos[Pt{x, y}]; e != nil {
			return e
		}
		x += dx
		y += dy
	}
	return nil
}

// enemyOnAxis checks if rotating to dir d would put any enemy on our axis.
// For speculative shots: only if wall is directly adjacent, single-thick,
// and no other walls between it and the enemy.
func (w *World) enemyOnAxis(me Tank, d Direction) (*Tank, int) {
	dx, dy := dd(d)
	fx, fy := me.X+dx, me.Y+dy
	// case 1: first cell is open — clear LOS scan
	if !w.Wall[Pt{fx, fy}] {
		x, y := fx, fy
		for x > 0 && x < w.R.Map.W-1 && y > 0 && y < w.R.Map.H-1 {
			if w.Wall[Pt{x, y}] {
				return nil, 0
			}
			if e := w.Epos[Pt{x, y}]; e != nil {
				return e, dist(Pt{me.X, me.Y}, Pt{e.X, e.Y})
			}
			x += dx
			y += dy
		}
		return nil, 0
	}
	// case 2: wall directly adjacent — check it's single-thick
	bx, by := fx+dx, fy+dy
	if w.Wall[Pt{bx, by}] {
		return nil, 0
	}
	// trace from behind the wall — enemy must appear before any other wall
	x, y := bx, by
	for x > 0 && x < w.R.Map.W-1 && y > 0 && y < w.R.Map.H-1 {
		if w.Wall[Pt{x, y}] {
			return nil, 0 // second wall — no shot
		}
		if e := w.Epos[Pt{x, y}]; e != nil {
			return e, dist(Pt{me.X, me.Y}, Pt{e.X, e.Y})
		}
		x += dx
		y += dy
	}
	return nil, 0
}

// ───────────────────────────── Escape: fire line + radiation ─────────────────────────────

func (w *World) escapeAction(me Tank, medTarget *Pt) string {
	myP := Pt{me.X, me.Y}
	inFireLane := w.FireLane[myP]
	inRad := w.irrad(me.X, me.Y)
	radSoon := !inRad && w.futureIrrad(me.X, me.Y, 40) // leave 40 ticks early
	bulletSoon := false
	if bt, ok := w.Danger[myP]; ok && bt <= 2 {
		bulletSoon = true
	}

	if !inFireLane && !inRad && !radSoon && !bulletSoon {
		return ""
	}

	type opt struct {
		act   string
		now   bool
		score int
	}
	var opts []opt
	for _, d := range allDirs {
		dx, dy := dd(d)
		nx, ny := me.X+dx, me.Y+dy
		if w.blocked(nx, ny) {
			continue
		}
		np := Pt{nx, ny}
		score := 0

		// leaving fire lane = big bonus
		if inFireLane && !w.FireLane[np] {
			score += 100
		}
		// entering fire lane = penalty
		if !inFireLane && w.FireLane[np] {
			score -= 40
		}
		// still in fire lane = small penalty
		if w.FireLane[np] {
			score -= 20
		}

		// leaving radiation = huge bonus
		if inRad && !w.irrad(nx, ny) {
			score += 150
		}
		// entering radiation = huge penalty
		if !inRad && w.irrad(nx, ny) {
			score -= 200
		}

		// moving away from future radiation = bonus
		if radSoon && !w.futureIrrad(nx, ny, 40) {
			score += 80
		}
		if radSoon && w.futureIrrad(nx, ny, 40) && !w.futureIrrad(me.X, me.Y, 40) {
			score -= 60 // don't move INTO future radiation
		}

		// dodging bullet = big bonus
		if bulletSoon {
			if _, hit := w.Danger[np]; !hit {
				score += 120
			} else {
				score -= 80
			}
		}

		// toward medkit = bonus
		if medTarget != nil {
			oldD := dist(Pt{me.X, me.Y}, *medTarget)
			newD := dist(np, *medTarget)
			if newD < oldD {
				score += 30
			}
		}

		// toward center = bonus (center is always safe from radiation longest)
		oldC := dist(Pt{me.X, me.Y}, Pt{w.CX, w.CY})
		newC := dist(np, Pt{w.CX, w.CY})
		if newC < oldC {
			score += 15
		}

		canNow := me.Dir == d
		if canNow {
			opts = append(opts, opt{"move_" + string(d), true, score})
		} else {
			opts = append(opts, opt{"rotate_" + string(d), false, score})
		}
	}

	// pick best: heavy weight for can-move-now
	best := ""
	bestVal := math.MinInt32
	for _, o := range opts {
		v := o.score
		if o.now {
			v += 60 // strong preference for instant escape
		}
		if v > bestVal {
			bestVal = v
			best = o.act
		}
	}
	return best
}

// ───────────────────────────── Medkit finding ─────────────────────────────

func (w *World) bestMedkit(me Tank) *Pt {
	var best *Pt
	bestD := math.MaxInt32
	for _, p := range w.R.Pickups {
		if w.irrad(p.X, p.Y) {
			continue
		}
		d := dist(Pt{me.X, me.Y}, Pt{p.X, p.Y})
		if w.futureIrrad(p.X, p.Y, d+5) {
			continue
		}
		if d < bestD {
			bestD = d
			pt := Pt{p.X, p.Y}
			best = &pt
		}
	}
	return best
}

// ───────────────────────────── Approach enemy from BEHIND ─────────────────────────────

func (w *World) behindPoint(e Tank) Pt {
	// "behind" = opposite of where enemy is facing
	dx, dy := dd(opposite(e.Dir))
	// try 3, 4, 2, 5 cells behind
	for _, off := range []int{3, 4, 2, 5, 6} {
		bx, by := e.X+dx*off, e.Y+dy*off
		if bx > 0 && bx < 19 && by > 0 && by < 19 && !w.Wall[Pt{bx, by}] && !w.irrad(bx, by) {
			return Pt{bx, by}
		}
	}
	// fallback: just go to enemy position
	return Pt{e.X, e.Y}
}

// ───────────────────────────── Main decision engine ─────────────────────────────

func decide(r Req) string {
	w := newWorld(r)
	me := r.Me
	if !me.Ok {
		return "idle"
	}

	myP := Pt{me.X, me.Y}

	// ── Track enemies (detect AFK) ──
	for _, e := range r.Enemies {
		if !e.Ok {
			continue
		}
		ep := Pt{e.X, e.Y}
		if last, ok := lastEPos[e.ID]; ok && last == ep {
			eStatic[e.ID]++
		} else {
			eStatic[e.ID] = 0
		}
		lastEPos[e.ID] = ep
	}

	// ── Anti-oscillation ──
	oscillating := tickN >= 2 && myP == prevPrevPos && myP != prevPos
	prevPrevPos = prevPos
	prevPos = myP
	tickN++

	// ── Find nearest safe medkit ──
	med := w.bestMedkit(me)

	// ── Check if radiation is closing in everywhere around us ──
	// If all 4 adjacent cells + our cell are irradiated (or blocked),
	// we're trapped in radiation → medkits are the only way to survive
	radEverywhere := w.irrad(me.X, me.Y)
	if radEverywhere {
		safeNeighbor := false
		for _, d := range allDirs {
			dx, dy := dd(d)
			nx, ny := me.X+dx, me.Y+dy
			if !w.blocked(nx, ny) && !w.irrad(nx, ny) {
				safeNeighbor = true
				break
			}
		}
		if !safeNeighbor {
			radEverywhere = true
		} else {
			radEverywhere = false
		}
	}

	// ════════════════════════════════════════════════════════
	// RADIATION EVERYWHERE: medkits are our only lifeline
	// Override everything — chase any medkit to outlast radiation
	// Also accept medkits IN radiation (better +50 HP than nothing)
	// ════════════════════════════════════════════════════════
	if radEverywhere {
		// try safe medkit first
		if med != nil {
			if a := w.moveTo(me, *med); a != "" {
				return a
			}
		}
		// no safe medkit — go for ANY medkit, even in radiation
		var anyMed *Pt
		anyMedD := math.MaxInt32
		for _, p := range r.Pickups {
			d := dist(myP, Pt{p.X, p.Y})
			if d < anyMedD {
				anyMedD = d
				pt := Pt{p.X, p.Y}
				anyMed = &pt
			}
		}
		if anyMed != nil {
			if a := w.moveTo(me, *anyMed); a != "" {
				return a
			}
		}
		// no medkits at all — move toward center (last to be irradiated)
		if a := w.moveTo(me, Pt{w.CX, w.CY}); a != "" {
			return a
		}
	}

	// ── Find best enemy target (prefer low HP, prefer close) ──
	var bestEnemy *Tank
	bestEScore := math.MaxInt32
	for i := range r.Enemies {
		e := &r.Enemies[i]
		if !e.Ok {
			continue
		}
		d := dist(myP, Pt{e.X, e.Y})
		score := d + e.HP/10
		if eStatic[e.ID] >= 5 {
			score -= 10 // AFK = free kill
		}
		if score < bestEScore {
			bestEScore = score
			bestEnemy = e
		}
	}

	// ════════════════════════════════════════════════════════
	// PRIORITY 1: ESCAPE — fire line, bullets, radiation
	// FIRST thing we do — get safe before anything else,
	// even before shooting (can't shoot if dead)
	// ════════════════════════════════════════════════════════
	if esc := w.escapeAction(me, med); esc != "" {
		return esc
	}

	// ════════════════════════════════════════════════════════
	// ALWAYS: Shoot if we can — clear LOS
	// ════════════════════════════════════════════════════════
	if t := w.clearLOS(me); t != nil {
		return "shoot"
	}

	// ════════════════════════════════════════════════════════
	// WALL ATTACK (defensive priority): if we are safe behind a wall
	// and can shoot through it, DO IT. The wall protects us while
	// we deal damage. Applies to current facing + rotation.
	// Rules: adjacent wall, single-thick, only one wall to enemy.
	// ════════════════════════════════════════════════════════
	if t := w.aggroLOS(me); t != nil {
		return "shoot"
	}
	// Also try rotating to get a wall-shot — we stay protected
	{
		bestDir := Direction("")
		bestS := math.MaxInt32
		for _, d := range allDirs {
			if d == me.Dir {
				continue
			}
			dx, dy := dd(d)
			fx, fy := me.X+dx, me.Y+dy
			// only consider if first cell IS a wall (we stay behind cover)
			if !w.Wall[Pt{fx, fy}] {
				continue
			}
			if e, ed := w.enemyOnAxis(me, d); e != nil {
				s := ed + e.HP/10
				if s < bestS {
					bestS = s
					bestDir = d
				}
			}
		}
		if bestDir != "" {
			return "rotate_" + string(bestDir)
		}
	}

	// ════════════════════════════════════════════════════════
	// CHECK: does any living enemy have more HP than us?
	// If yes → medkits become PRIORITY 1 (must heal before fighting)
	// ════════════════════════════════════════════════════════
	outgunned := false
	for _, e := range r.Enemies {
		if e.Ok && e.HP > me.HP {
			outgunned = true
			break
		}
	}

	// ════════════════════════════════════════════════════════
	// PRIORITY 1b: OUTGUNNED — enemy has more HP → find medkit NOW
	// We cannot win a fair trade, must heal first
	// ════════════════════════════════════════════════════════
	if outgunned && med != nil {
		if a := w.moveTo(me, *med); a != "" {
			return a
		}
	}

	// ════════════════════════════════════════════════════════
	// PRIORITY 2: MEDKITS — always go for healing
	// At any HP: close medkit is always worth grabbing (+50 HP is huge)
	// At low HP: go for any medkit, any distance
	// ════════════════════════════════════════════════════════
	if med != nil {
		lowHP := me.HP <= 40
		medD := dist(myP, *med)
		goForIt := false
		if lowHP {
			goForIt = true // any distance when dying
		} else if medD <= 12 {
			goForIt = true // always grab if close
		} else if me.HP <= 80 && medD <= 18 {
			goForIt = true // injured + medium range
		} else if me.HP <= 60 {
			goForIt = true // hurt = go for it
		}
		if goForIt {
			if a := w.moveTo(me, *med); a != "" {
				return a
			}
		}
	}

	// LOW HP + no medkit: retreat to center and wait
	if me.HP <= 40 {
		if me.X != w.CX || me.Y != w.CY {
			if a := w.moveTo(me, Pt{w.CX, w.CY}); a != "" {
				return a
			}
		}
		return "idle"
	}

	// OUTGUNNED + no medkit: retreat to center and wait for spawn
	if outgunned && med == nil {
		if me.X != w.CX || me.Y != w.CY {
			if a := w.moveTo(me, Pt{w.CX, w.CY}); a != "" {
				return a
			}
		}
		return "idle"
	}

	// ════════════════════════════════════════════════════════
	// ROTATE GUN TOWARD ENEMY — open LOS only (wall shots
	// already handled above at higher priority)
	// ════════════════════════════════════════════════════════
	{
		bestDir := Direction("")
		bestS := math.MaxInt32
		for _, d := range allDirs {
			if d == me.Dir {
				continue
			}
			dx, dy := dd(d)
			fx, fy := me.X+dx, me.Y+dy
			// skip if first cell is a wall (wall-shots handled above)
			if w.Wall[Pt{fx, fy}] {
				continue
			}
			if e, ed := w.enemyOnAxis(me, d); e != nil {
				s := ed + e.HP/10
				if s < bestS {
					bestS = s
					bestDir = d
				}
			}
		}
		if bestDir != "" {
			if w.cellDanger(me.X, me.Y) == 0 {
				return "rotate_" + string(bestDir)
			}
		}
	}

	// ════════════════════════════════════════════════════════
	// HP ADVANTAGE: only attack head-on if we have MORE HP
	// Rush directly at them — we win the trade
	// ════════════════════════════════════════════════════════
	if bestEnemy != nil && me.HP > bestEnemy.HP {
		tgt := Pt{bestEnemy.X, bestEnemy.Y}
		if a := w.moveTo(me, tgt); a != "" {
			return a
		}
	}

	// ════════════════════════════════════════════════════════
	// HUNT: approach enemy from BEHIND, not front
	// Safer + enemy has to rotate before they can shoot back
	// This is the ONLY way we approach when enemy has >= our HP
	// ════════════════════════════════════════════════════════
	if bestEnemy != nil {
		behind := w.behindPoint(*bestEnemy)
		if a := w.moveTo(me, behind); a != "" {
			return a
		}
		// fallback: go toward them ONLY if we have more HP
		if me.HP > bestEnemy.HP {
			if a := w.moveTo(me, Pt{bestEnemy.X, bestEnemy.Y}); a != "" {
				return a
			}
		}
	}

	// ════════════════════════════════════════════════════════
	// DEFAULT: Move toward center (best position on the map)
	// ════════════════════════════════════════════════════════
	if me.X != w.CX || me.Y != w.CY {
		if a := w.moveTo(me, Pt{w.CX, w.CY}); a != "" {
			return a
		}
	}

	// ════════════════════════════════════════════════════════
	// ANTI-OSCILLATION: break A→B→A loops
	// ════════════════════════════════════════════════════════
	if oscillating {
		dx, dy := dd(me.Dir)
		nx, ny := me.X+dx, me.Y+dy
		if !w.blocked(nx, ny) {
			return "move_" + string(me.Dir)
		}
		for _, d := range allDirs {
			if d == me.Dir {
				continue
			}
			dx2, dy2 := dd(d)
			if !w.blocked(me.X+dx2, me.Y+dy2) {
				return "rotate_" + string(d)
			}
		}
	}

	return "idle"
}

// ───────────────────────────── HTTP Server ─────────────────────────────

func main() {
	port := flag.Int("port", 8080, "bot server port")
	flag.Parse()

	http.HandleFunc("/action", func(wr http.ResponseWriter, hr *http.Request) {
		if hr.Method != http.MethodPost {
			http.Error(wr, "post only", http.StatusMethodNotAllowed)
			return
		}
		var r Req
		if err := json.NewDecoder(hr.Body).Decode(&r); err != nil {
			http.Error(wr, "bad", http.StatusBadRequest)
			return
		}
		a := decide(r)
		wr.Header().Set("Content-Type", "application/json")
		json.NewEncoder(wr).Encode(Resp{Action: a})
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[bot] listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
