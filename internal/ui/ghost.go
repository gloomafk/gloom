package ui

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	pending bool // one tick may still arrive after the welcome becomes hidden
}

func ghostCmd() tea.Cmd {
	return tea.Tick(ghostInterval, func(t time.Time) tea.Msg { return ghostTick(t) })
}

func (m *Model) scheduleGhost() tea.Cmd {
	if m.ghost.pending || !m.ghostVisible() {
		return nil
	}
	m.ghost.pending = true
	return ghostCmd()
}

func (m *Model) ghostVisible() bool {
	if m.quitting || m.starting || m.ghost.frozen || !m.showing(0) || !m.fitsLayout() || m.sel.down {
		return false
	}
	w := m.windows[0]
	// The welcome is the first line until it is cleared or evicted.
	if w.offset != 0 || len(w.lines) == 0 || w.lines[0].ghost != &m.ghost {
		return false
	}
	width := m.width
	if m.channels && width-m.clCols >= 30 {
		width -= m.clCols
	}
	textWidth := max(1, width-m.messagePrefixWidth()-1)
	welcomeRows := strings.Count(ansi.Wrap(w.lines[0].text, textWidth, " "), "\n") + 1
	if !m.ghostFits(width, welcomeRows) {
		return false
	}
	rows, _ := m.cachedMessageRows(w, m.colors(), width)
	// Later status messages can push the entire silhouette above the viewport.
	ghostEnd := welcomeRows + m.ghost.padding + len(ghostMask)
	return len(rows)-(m.pageRows()+1) < ghostEnd
}

func (m *Model) ghostFits(width, precedingRows int) bool {
	return width >= ghostWidth && m.pageRows()+1 >= precedingRows+21
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
		var row, run strings.Builder
		run.WriteString(strings.Repeat(" ", max(0, (width-ghostWidth)/2)))
		for x := range ghostWidth {
			if !ghostOccupied(x, y) {
				run.WriteByte(' ')
				continue
			}
			c := cells[y][x]
			if c == 0 {
				seed := float64(x*31 + y*17)
				step := g.frame / (2 + int(ghostRandom(seed)*6))
				c = dense[int(ghostRandom(seed+float64(step)*7.3)*float64(len(dense)))]
			}
			if accents[y][x] {
				row.WriteString(plain.Render(run.String()))
				run.Reset()
				row.WriteString(accent.Render(string(c)))
			} else {
				run.WriteByte(c)
			}
		}
		row.WriteString(plain.Render(run.String()))
		rows = append(rows, fit(row.String(), width, p.bg))
	}
	for range g.padding {
		rows = append(rows, fit("", width, p.bg))
	}
	return rows
}
