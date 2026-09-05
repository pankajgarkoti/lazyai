// Package highlight tokenises source files with Chroma and renders them with
// Lip Gloss so colours degrade gracefully on terminals without true colour.
//
// Output is structured per line so callers can lay a background tint under a
// row (diff add/delete, the Show target line) without the tint being undone
// by the reset that follows each token.
package highlight

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"

	"lazyai/internal/theme"
)

// Span is a run of text sharing one style.
type Span struct {
	Text  string
	Style lipgloss.Style
	// styled is false for pass-through text so Render can skip Lip Gloss
	// entirely when neither a token style nor a background applies.
	styled bool
}

// Line is one source line as styled spans; the concatenated Text equals the
// original line without its newline.
type Line []Span

// Plain returns the unstyled text.
func (l Line) Plain() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Render emits the line as ANSI. When bg is non-nil every span carries it.
func (l Line) Render(bg lipgloss.TerminalColor) string {
	var b strings.Builder
	for _, s := range l {
		switch {
		case bg != nil:
			b.WriteString(s.Style.Background(bg).Render(s.Text))
		case s.styled:
			b.WriteString(s.Style.Render(s.Text))
		default:
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// Cut splits the line before rune index i (clamped to the line length),
// preserving span styles on both sides.
func (l Line) Cut(i int) (Line, Line) {
	if i <= 0 {
		return nil, l
	}
	var head Line
	for k, s := range l {
		n := len([]rune(s.Text))
		if i >= n {
			head = append(head, s)
			i -= n
			continue
		}
		r := []rune(s.Text)
		if i > 0 {
			head = append(head, Span{Text: string(r[:i]), Style: s.Style, styled: s.styled})
		}
		tail := Line{{Text: string(r[i:]), Style: s.Style, styled: s.styled}}
		return head, append(tail, l[k+1:]...)
	}
	return head, nil
}

// Highlighter maps Chroma tokens to Lip Gloss styles for one renderer.
type Highlighter struct {
	r     *lipgloss.Renderer
	style *chroma.Style
	mu    sync.Mutex
	cache map[chroma.TokenType]cached
}

type cached struct {
	style  lipgloss.Style
	styled bool
}

// Option configures a Highlighter.
type Option func(*Highlighter)

// WithRenderer uses a specific Lip Gloss renderer (tests, alternate outputs).
func WithRenderer(r *lipgloss.Renderer) Option { return func(h *Highlighter) { h.r = r } }

// WithStyle selects a Chroma style by name; unknown names keep the default.
func WithStyle(name string) Option {
	return func(h *Highlighter) {
		if s := styles.Get(name); s != nil && s.Name == name {
			h.style = s
		}
	}
}

// New builds a Highlighter using the theme's Chroma style.
func New(opts ...Option) *Highlighter {
	h := &Highlighter{
		r:     lipgloss.DefaultRenderer(),
		style: styles.Get(theme.ChromaStyle),
		cache: map[chroma.TokenType]cached{},
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

var defaultHL = struct {
	once sync.Once
	h    *Highlighter
}{}

// Default returns the process-wide Highlighter bound to the default renderer.
func Default() *Highlighter {
	defaultHL.once.Do(func() { defaultHL.h = New() })
	return defaultHL.h
}

// File highlights src using the lexer chosen for path. Line splitting follows
// strings.Split semantics after trimming one trailing newline, so "a\nb\n" and
// "a\nb" both yield two lines and "" yields none.
func File(path, src string) []Line { return Default().File(path, src) }

// File is the method form of the package-level File.
func (h *Highlighter) File(path, src string) []Line {
	if src == "" {
		return nil
	}
	src = strings.TrimSuffix(src, "\n")
	lexer := lexers.Match(path)
	if lexer == nil {
		return passThrough(src)
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, src+"\n")
	if err != nil {
		return passThrough(src)
	}
	lines := []Line{{}}
	for tok := it(); tok != chroma.EOF; tok = it() {
		if tok.Value == "" {
			continue
		}
		st, styled := h.styleFor(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				lines = append(lines, Line{})
			}
			if p == "" {
				continue
			}
			cur := &lines[len(lines)-1]
			*cur = append(*cur, Span{Text: p, Style: st, styled: styled})
		}
	}
	// The trailing "\n" we appended opens one empty line past the end.
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	// Chroma may normalise line endings; fall back if the shape changed.
	if want := strings.Count(src, "\n") + 1; len(lines) != want {
		return passThrough(src)
	}
	return lines
}

func passThrough(src string) []Line {
	raw := strings.Split(src, "\n")
	out := make([]Line, len(raw))
	for i, l := range raw {
		if l != "" {
			out[i] = Line{{Text: l}}
		} else {
			out[i] = Line{}
		}
	}
	return out
}

// styleFor converts a Chroma style entry into a Lip Gloss style (foreground
// and attributes only; token backgrounds are dropped so the terminal stays
// transparent, like the author's Neovim setup).
func (h *Highlighter) styleFor(t chroma.TokenType) (lipgloss.Style, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.cache[t]; ok {
		return c.style, c.styled
	}
	e := h.style.Get(t)
	s := h.r.NewStyle()
	styled := false
	if e.Colour.IsSet() {
		s = s.Foreground(lipgloss.Color(e.Colour.String()))
		styled = true
	}
	if e.Bold == chroma.Yes {
		s = s.Bold(true)
		styled = true
	}
	if e.Italic == chroma.Yes {
		s = s.Italic(true)
		styled = true
	}
	if e.Underline == chroma.Yes {
		s = s.Underline(true)
		styled = true
	}
	h.cache[t] = cached{style: s, styled: styled}
	return s, styled
}
