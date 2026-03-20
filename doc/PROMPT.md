## Claude Code Prompt

Use this prompt with Claude Code to generate a competitive bot. Copy it as-is:

```
Read the file BOT.md for the full game mechanics and protocol specification.

Create a Tank Battle bot in Go (standard library only, no external dependencies).
The project should be in a new directory bot/ with main.go and go.mod.
It must compile with `GOOS=linux GOARCH=amd64 go build -o bot .`.

Write a competitive bot with smart strategy:
1. Dodge incoming bullets (check if any enemy bullet is heading directly at me within 3-4 cells)
2. Flee radiation zone when standing in it (move toward map center)
3. Pick up medkits when HP is low and no enemy in line of sight
4. Shoot enemies on same row/column with clear line of sight (no walls between us)
5. Move toward closest enemy using pathfinding that avoids walls
6. Use walls as cover when possible
7. Avoid getting cornered

The bot must respond within 100ms. Keep logic efficient.
```

## Tips

- **Timeout = death**: if your bot takes >100ms, it idles. Keep logic simple and avoid allocations in hot paths.
- **Rotation costs a tick**: plan your movements to minimize unnecessary rotations.
- **Bullets are fast**: 2 cells/tick means a bullet 4 cells away hits in 2 ticks. Dodge early.
- **Radiation is inevitable**: don't camp in corners -- the zone shrinks. By tick 280, only the center 2x2 is safe.
- **Medkits are powerful**: +50 HP with no cap. A tank with 150 HP survives 4 hits instead of 3.
- **Walls block bullets**: use them as cover when approaching enemies.
- **No friendly fire**: bullets only hit enemy tanks, but don't waste shots on walls.
- **Map is static per match**: parse walls once and cache the wall set for O(1) lookups.