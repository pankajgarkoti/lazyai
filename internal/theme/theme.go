// Package theme is the single source of truth for LazyAI's look.
//
// Chrome (status bar, borders, selections) uses the same three-colour
// "blue_mist" palette as the author's tmux and lualine configs; content
// (markers, diff signs, source code) uses Catppuccin Mocha so syntax colours
// match Neovim. Glyphs assume a Nerd Font.
package theme

import (
	"path"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Chrome palette (blue_mist).
const (
	ChromeBG     = lipgloss.Color("#1e3a4a")
	ChromeFG     = lipgloss.Color("#c5d7e5")
	ChromeAccent = lipgloss.Color("#2a82b5")
)

// Content palette (Catppuccin Mocha).
const (
	Text     = lipgloss.Color("#cdd6f4")
	Subtext  = lipgloss.Color("#a6adc8")
	Overlay  = lipgloss.Color("#6c7086")
	Surface0 = lipgloss.Color("#313244")
	Surface1 = lipgloss.Color("#45475a")
	Blue     = lipgloss.Color("#89b4fa")
	Sapphire = lipgloss.Color("#74c7ec")
	Sky      = lipgloss.Color("#89dceb")
	Teal     = lipgloss.Color("#94e2d5")
	Green    = lipgloss.Color("#a6e3a1")
	Yellow   = lipgloss.Color("#f9e2af")
	Peach    = lipgloss.Color("#fab387")
	Red      = lipgloss.Color("#f38ba8")
	Mauve    = lipgloss.Color("#cba6f7")
	Pink     = lipgloss.Color("#f5c2e7")
	Lavender = lipgloss.Color("#b4befe")

	// Diff line tints: Mocha base blended toward green / red.
	DiffAddBG = lipgloss.Color("#2a3d34")
	DiffDelBG = lipgloss.Color("#3d2a33")
)

// Glyphs.
const (
	Sep          = "⸗"      // the double-oblique hyphen used as block separator in tmux/lualine
	Badge        = "▼・ᴥ・▼"  // status-left bear
	Gradient     = "░▒▓"    // lualine search-count lead-in
	Dot          = "●"      // plugin connected
	Ring         = "○"      // plugin waiting
	Spinner      = "⟳"      // workstream busy (tool call in flight)
	Modified     = "•"      // lualine modified marker
	IconFiles    = "\uf07c" //
	IconShow     = "\uf002" //
	IconFold     = "\uf07b" //
	IconPlug     = "\uf1e6" //
	IconWarn     = "\uf071" //
	IconInfo     = "\uf05a" //
	IconHunk     = "\uf0c1" //
	IconZoom     = "\uf065" //
	IconBranch   = "\ue0a0" //  (lualine branch glyph)
	IconWorktree = "\uf1bb" //
	IconHelp     = "\uf128" //
	// Diagnostic float glyphs (single border like the author's Neovim floats).
	FloatTL, FloatTR, FloatBL, FloatBR = "┌", "┐", "└", "┘"
	FloatH, FloatV                     = "─", "│"
	ChromaStyle                        = "catppuccin-mocha"
)

// Border shape used for both panes.
var Border = lipgloss.RoundedBorder()

// Pane borders.
var (
	BorderFocused   = lipgloss.NewStyle().Border(Border).BorderForeground(ChromeAccent)
	BorderUnfocused = lipgloss.NewStyle().Border(Border).BorderForeground(ChromeBG)
)

// Status bar segments, modelled on the patched Dracula tmux theme.
var (
	StatusBar   = lipgloss.NewStyle().Foreground(ChromeFG).Background(ChromeBG)
	StatusDim   = lipgloss.NewStyle().Foreground(Overlay).Background(ChromeBG)
	StatusKey   = lipgloss.NewStyle().Foreground(ChromeAccent).Background(ChromeBG).Bold(true)
	BadgeBlock  = lipgloss.NewStyle().Foreground(ChromeBG).Background(ChromeAccent).Bold(true)
	SepOnBG     = lipgloss.NewStyle().Foreground(ChromeAccent).Background(ChromeBG)
	SepOnAccent = lipgloss.NewStyle().Foreground(ChromeBG).Background(ChromeAccent)
	TabActive   = lipgloss.NewStyle().Foreground(ChromeFG).Background(ChromeAccent).Bold(true)
	AccentBlock = lipgloss.NewStyle().Foreground(ChromeBG).Background(ChromeAccent)
	Notice      = lipgloss.NewStyle().Foreground(Peach).Background(ChromeBG)
	ModeNormal  = lipgloss.NewStyle().Foreground(ChromeBG).Background(Blue).Bold(true)
	ModeInsert  = lipgloss.NewStyle().Foreground(ChromeBG).Background(Green).Bold(true)
	ModeTerm    = lipgloss.NewStyle().Foreground(ChromeBG).Background(Teal).Bold(true)
	ModeDiff    = lipgloss.NewStyle().Foreground(ChromeBG).Background(Peach).Bold(true)
	ModeShow    = lipgloss.NewStyle().Foreground(ChromeBG).Background(Mauve).Bold(true)
)

// Sidebar and content text.
var (
	Title   = lipgloss.NewStyle().Bold(true).Foreground(ChromeAccent)
	Dim     = lipgloss.NewStyle().Foreground(Overlay)
	Warn    = lipgloss.NewStyle().Foreground(Peach)
	SelLine = lipgloss.NewStyle().Foreground(ChromeFG).Background(ChromeAccent).Bold(true)
	SelDim  = lipgloss.NewStyle().Foreground(Text).Background(Surface1)
	Index   = lipgloss.NewStyle().Foreground(Overlay)
	Icon    = lipgloss.NewStyle().Foreground(Sapphire)

	// Sidebar file-status markers.
	Marks = map[string]lipgloss.Style{
		"M": lipgloss.NewStyle().Foreground(Peach),
		"A": lipgloss.NewStyle().Foreground(Green),
		"D": lipgloss.NewStyle().Foreground(Red),
		"R": lipgloss.NewStyle().Foreground(Blue),
		"S": lipgloss.NewStyle().Foreground(Mauve),
		" ": lipgloss.NewStyle(),
	}
)

// Diff and source view.
var (
	DiffHeader  = lipgloss.NewStyle().Bold(true).Foreground(ChromeAccent)
	DiffHunk    = lipgloss.NewStyle().Foreground(Sapphire).Italic(true)
	DiffAddSign = lipgloss.NewStyle().Foreground(Green).Background(DiffAddBG).Bold(true)
	DiffDelSign = lipgloss.NewStyle().Foreground(Red).Background(DiffDelBG).Bold(true)
	DiffCtxSign = lipgloss.NewStyle().Foreground(Overlay)
	DiffAddLine = lipgloss.NewStyle().Background(DiffAddBG)
	DiffDelLine = lipgloss.NewStyle().Background(DiffDelBG)

	LineNo       = lipgloss.NewStyle().Foreground(Overlay)
	LineNoTarget = lipgloss.NewStyle().Foreground(Peach).Bold(true)
	TargetLine   = lipgloss.NewStyle().Background(Surface0)
	TargetCol    = lipgloss.NewStyle().Reverse(true)
)

// Diagnostic-style floats: transparent body, border in the text colour (the
// author links FloatBorder to Normal), severity icon coloured like Mocha's
// DiagnosticInfo / DiagnosticWarn.
var (
	FloatBorder = lipgloss.NewStyle().Foreground(Text)
	FloatText   = lipgloss.NewStyle().Foreground(Text)
	FloatFooter = lipgloss.NewStyle().Foreground(Overlay).Italic(true)
	DiagInfo    = lipgloss.NewStyle().Foreground(Sky)
	DiagWarn    = lipgloss.NewStyle().Foreground(Yellow)
	PromptText  = lipgloss.NewStyle().Foreground(Text)
	PromptLabel = lipgloss.NewStyle().Foreground(ChromeAccent).Bold(true)
)

// FileIcon returns a Nerd Font devicon for a path based on its extension or
// well-known basename.
func FileIcon(p string) string {
	lower := strings.ToLower(path.Base(p))
	switch lower {
	case "makefile", "gnumakefile":
		return "\ue779"
	case "dockerfile":
		return "\uf308"
	case "license", "licence":
		return "\uf718"
	case ".gitignore", ".gitattributes":
		return "\uf1d3"
	case "go.mod", "go.sum":
		return "\ue627"
	}
	switch strings.TrimPrefix(path.Ext(lower), ".") {
	case "go":
		return "\ue627"
	case "ts", "tsx":
		return "\ue628"
	case "js", "jsx", "mjs", "cjs":
		return "\ue74e"
	case "lua":
		return "\ue620"
	case "py":
		return "\ue73c"
	case "rs":
		return "\ue7a8"
	case "rb":
		return "\ue791"
	case "sh", "bash", "zsh", "fish":
		return "\uf489"
	case "md", "mdx", "markdown":
		return "\uf48a"
	case "json", "jsonc":
		return "\ue60b"
	case "yaml", "yml":
		return "\ue6a8"
	case "toml":
		return "\ue6b2"
	case "html", "htm":
		return "\ue736"
	case "css", "scss", "sass":
		return "\ue749"
	case "svelte":
		return "\ue697"
	case "vue":
		return "\ue6a0"
	case "c", "h":
		return "\ue61e"
	case "cc", "cpp", "hpp", "cxx":
		return "\ue61d"
	case "java":
		return "\ue738"
	case "kt", "kts":
		return "\ue634"
	case "swift":
		return "\ue755"
	case "sql":
		return "\ue706"
	case "txt":
		return "\uf15c"
	case "lock":
		return "\uf023"
	case "png", "jpg", "jpeg", "gif", "svg", "webp":
		return "\uf1c5"
	case "zip", "tar", "gz", "tgz":
		return "\uf410"
	}
	return "\uf15b"
}
