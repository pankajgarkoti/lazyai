package terminal

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

const reset = "\x1b[0m"

// fadeBase is what colours blend toward when a pane is out of focus: a dark
// neutral close to the author's terminal background.
var fadeBase = color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}

// fadeDefaultFg stands in for the terminal's default foreground when fading,
// so plain text dims too instead of staying bright.
var fadeDefaultFg = color.RGBA{R: 0xcd, G: 0xd6, B: 0xf4, A: 0xff}

// blend mixes c toward fadeBase by t (0 keeps c, 1 gives fadeBase). Indexed
// ANSI colours resolve through their RGBA() using the default palette, so
// the result is always a concrete RGB colour.
func blend(c color.Color, t float64) color.Color {
	if c == nil {
		return nil
	}
	r, g, b, _ := c.RGBA()
	mix := func(v uint32, base uint8) uint8 {
		return uint8((float64(v>>8)*(1-t) + float64(base)*t))
	}
	return color.RGBA{R: mix(r, fadeBase.R), G: mix(g, fadeBase.G), B: mix(b, fadeBase.B), A: 0xff}
}

// fade returns the out-of-focus version of a cell style.
func fade(s uv.Style) uv.Style {
	fg := s.Fg
	if fg == nil {
		fg = fadeDefaultFg
	}
	s.Fg = blend(fg, 0.55)
	if s.Bg != nil {
		s.Bg = blend(s.Bg, 0.55)
	}
	s.Attrs |= uv.AttrFaint
	s.Attrs &^= uv.AttrBold
	return s
}

// render converts the emulator's visible screen into one ANSI string per row.
// Rows are padded to the full width so the host can place them in a pane
// without measuring, and every row ends with a full reset so styles never
// bleed into neighbouring panes.
func render(emu *vt.Emulator, showCursor, faded bool) []string {
	w, h := emu.Width(), emu.Height()
	cur := emu.CursorPosition()
	rows := make([]string, 0, h)

	var sb strings.Builder
	for y := 0; y < h; y++ {
		sb.Reset()
		var pen uv.Style
		for x := 0; x < w; {
			c := emu.CellAt(x, y)
			if c == nil {
				sb.WriteByte(' ')
				x++
				continue
			}
			width := c.Width
			if width <= 0 {
				// Continuation of a wide grapheme; already emitted.
				x++
				continue
			}
			style := c.Style
			if showCursor && x == cur.X && y == cur.Y {
				style.Attrs ^= uv.AttrReverse
			}
			if faded {
				style = fade(style)
			}
			if !style.Equal(&pen) {
				if style.IsZero() {
					sb.WriteString(reset)
				} else {
					sb.WriteString(style.Diff(&pen))
				}
				pen = style
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			sb.WriteString(content)
			x += width
		}
		sb.WriteString(reset)
		rows = append(rows, sb.String())
	}
	return rows
}
