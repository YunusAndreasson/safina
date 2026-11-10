package sections

import (
	"strings"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// rowCache keeps rendered list rows between frames. Styling a row costs
// lipgloss width measurement per cell, and a pod list redraws every visible
// row on every frame. A cursor move changes two rows and a refresh only the
// rows whose data changed, so the rest are reused.
//
// A key holds every input of a row's rendering (see rowKey), so a hit is
// always identical to rendering again. Only rows drawn by the previous frame
// are kept, which bounds the cache to one screen of rows.
type rowCache struct {
	theme      *theme.Theme
	prev, next map[string]string
}

// get returns the cached row for key, or renders and stores it.
func (c *rowCache) get(th *theme.Theme, key string, render func() string) string {
	if c.theme != th {
		c.theme, c.prev, c.next = th, nil, nil
	}
	if c.next == nil {
		c.next = make(map[string]string, len(c.prev))
	}
	row, ok := c.next[key]
	if !ok {
		if row, ok = c.prev[key]; !ok {
			row = render()
		}
		c.next[key] = row
	}
	return row
}

// endFrame drops rows the frame just drawn did not use.
func (c *rowCache) endFrame() {
	c.prev, c.next = c.next, nil
}

// rowKey joins a row's inputs with a separator no input contains.
func rowKey(parts ...string) string {
	return strings.Join(parts, "\x00")
}
