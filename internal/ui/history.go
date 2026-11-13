package ui

import "slices"

// rowCache retains formatted rows for one bounded history. ends records the
// exclusive row end of each source line, including its wrapped continuations.
type rowCache struct {
	first uint64 // sequence of the oldest source line
	ends  []int
	rows  []string
}

// trim releases expired lines and returns the number of removed display rows.
func (c *rowCache) trim(first uint64) int {
	if first == c.first {
		return 0
	}
	if first < c.first || first-c.first >= uint64(len(c.ends)) {
		removed := len(c.rows)
		*c = rowCache{first: first}
		return removed
	}
	dropped := int(first - c.first)
	removed := c.ends[dropped-1]
	clear(c.rows[:removed])
	c.rows = c.rows[removed:]
	c.ends = slices.Delete(c.ends, 0, dropped)
	for i := range c.ends {
		c.ends[i] -= removed
	}
	c.first = first
	return removed
}
