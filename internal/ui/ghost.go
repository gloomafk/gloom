package ui

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ghostMask is the 30 by 18 cell silhouette of the homepage ghost.
var ghostMask = [...]string{
	"      ###############         ",
	"      ###############         ",
	"   #####################      ",
	"   #####################      ",
	"###########################   ",
	"###########################   ",
	"######   #########   ######   ",
	"######   #########   ######   ",
	"######   #########   ######   ",
	"######   #########   ######   ",
	"###########################   ",
	"###########################   ",
	"###########################   ",
	"###########################   ",
	"#####################         ",
	"###########################   ",
	"   #########   ###############",
	"   #########   ###############",
}

// The homepage changes characters every 90 ms and places words for 1500 ms.
const ghostInterval = 90 * time.Millisecond

// Wider cells compensate for terminal line spacing while retaining 18 rows.
const ghostWidth = 42

func ghostOccupied(x, y int) bool {
	return ghostMask[y][x*len(ghostMask[y])/ghostWidth] == '#'
}

type ghostTick time.Time

type welcomeGhost struct {
	frame   int
	frozen  bool
	padding int
	shown   bool
}

func ghostCmd() tea.Cmd {
	return tea.Tick(ghostInterval, func(t time.Time) tea.Msg { return ghostTick(t) })
}

func ghostRandom(seed float64) float64 {
	v := math.Sin(seed*127.1+311.7) * 43758.5453
	return v - math.Floor(v)
}

func (g *welcomeGhost) rows(p palette, width int) []string {
	const dense = "hauntircfromyourterminal<gloom>boo/join#gloom"
	words := [...]string{"irc", "lol", "gloom", "afk"}
	var cells [18][ghostWidth]byte
	var accents [18][ghostWidth]bool
	tick := g.frame * 90
	for event := max(0, (tick-1500)/650); event <= tick/650; event++ {
		if tick-event*650 >= 1500 {
			continue
		}
		e := float64(event)
		word := words[int(ghostRandom(e*3.1)*float64(len(words)))]
		for attempt := range 12 {
			a := float64(attempt)
			y := int(ghostRandom(e*5.7+a) * 18)
			start := int(ghostRandom(e*9.3+a*1.7) * float64(ghostWidth+1-len(word)))
			fits := true
			for i := range len(word) {
				if !ghostOccupied(start+i, y) || cells[y][start+i] != 0 {
					fits = false
					break
				}
			}
			if !fits {
				continue
			}
			for i := range len(word) {
				cells[y][start+i] = word[i]
				accents[y][start+i] = word == "gloom"
			}
			break
		}
	}
	rows := make([]string, 0, 18+2*g.padding)
	for range g.padding {
		rows = append(rows, fit("", width, p.bg))
	}
	plain, accent := style(p.dim, p.bg), style(p.accent, p.bg).Bold(true)
	for y := range ghostMask {
		var row strings.Builder
		row.WriteString(strings.Repeat(" ", max(0, (width-ghostWidth)/2)))
		for x := range ghostWidth {
			if !ghostOccupied(x, y) {
				row.WriteByte(' ')
				continue
			}
			c := cells[y][x]
			if c == 0 {
				seed := float64(x*31 + y*17)
				step := g.frame / (2 + int(ghostRandom(seed)*6))
				c = dense[int(ghostRandom(seed+float64(step)*7.3)*float64(len(dense)))]
			}
			if accents[y][x] {
				row.WriteString(accent.Render(string(c)))
			} else {
				row.WriteString(plain.Render(string(c)))
			}
		}
		rows = append(rows, fit(row.String(), width, p.bg))
	}
	for range g.padding {
		rows = append(rows, fit("", width, p.bg))
	}
	return rows
}
