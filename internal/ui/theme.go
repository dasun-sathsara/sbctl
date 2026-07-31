// Package ui owns every visual surface sbctl presents: the theme tokens, the
// reusable components built from them, the pure render functions used by
// non-interactive commands, and the interactive profile picker.
//
// Rendering is separated from decision-making on purpose. Every function in
// render.go is pure — state in, string out — so the entire visual layer is
// verifiable with golden files and needs no terminal.
package ui

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Theme holds the resolved presentation tokens for one invocation.
//
// Colour degradation is deliberately not implemented here. lipgloss already
// resolves an ASCII profile when stdout is not a terminal or when NO_COLOR is
// set, and its Render then emits bare text. Re-implementing that detection
// would add code that duplicates behaviour we already get for free, so this
// type concerns itself only with what lipgloss cannot decide: which palette
// suits the background, which glyphs are safe, and how wide the output may be.
type Theme struct {
	// Plain replaces Unicode glyphs with ASCII and drops box borders. It is
	// for terminals that cannot render the glyphs, and for users who want
	// predictable output.
	Plain bool

	// Width is the usable terminal width. Zero means unknown, in which case
	// components avoid width-dependent decoration.
	Width int

	Accent  lipgloss.TerminalColor
	Success lipgloss.TerminalColor
	Warning lipgloss.TerminalColor
	Danger  lipgloss.TerminalColor
	Muted   lipgloss.TerminalColor
	Fg      lipgloss.TerminalColor
	Info    lipgloss.TerminalColor

	Symbols Symbols
}

// Symbols is the glyph set for state indication.
//
// Every state is conveyed by a glyph and, in list and status output, an
// accompanying word. Nothing relies on colour alone, so the output remains
// readable for colour-blind users, on monochrome terminals, and through a
// screen reader.
type Symbols struct {
	Active      string
	Inactive    string
	Success     string
	Failure     string
	Warning     string
	Cursor      string
	Bullet      string
	ArrowHint   string
	BorderStyle lipgloss.Border
}

// unicodeSymbols is the default glyph set.
var unicodeSymbols = Symbols{
	Active:      "●",
	Inactive:    "○",
	Success:     "✓",
	Failure:     "✗",
	Warning:     "⚠",
	Cursor:      "▸",
	Bullet:      "·",
	ArrowHint:   "→",
	BorderStyle: lipgloss.RoundedBorder(),
}

// asciiSymbols is the fallback glyph set for plain mode.
var asciiSymbols = Symbols{
	Active:      "*",
	Inactive:    "-",
	Success:     "[ok]",
	Failure:     "[!!]",
	Warning:     "[todo]",
	Cursor:      ">",
	Bullet:      "-",
	ArrowHint:   "->",
	BorderStyle: lipgloss.Border{},
}

// Palette tokens. Each is an AdaptiveColor so lipgloss can pick the variant
// that stays legible against the detected terminal background; the previous
// hardcoded ANSI indices were unreadable on light themes.
var (
	tokenAccent  = lipgloss.AdaptiveColor{Light: "#7c3aed", Dark: "#c4a7e7"}
	tokenSuccess = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#a6e3a1"}
	tokenWarning = lipgloss.AdaptiveColor{Light: "#a16207", Dark: "#f9e2af"}
	tokenDanger  = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f38ba8"}
	tokenMuted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#7f849c"}
	tokenFg      = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#cdd6f4"}
	tokenInfo    = lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#89b4fa"}
)

// MinPanelWidth is the narrowest terminal that can show bordered panels without
// the borders wrapping into noise.
const MinPanelWidth = 50

// Options selects presentation behaviour from the command line and environment.
type Options struct {
	// Plain forces ASCII glyphs and unbordered layout.
	Plain bool
	// NoColor disables colour while keeping Unicode glyphs.
	NoColor bool
}

// NewTheme resolves a Theme for the given output stream.
//
// It also applies NoColor globally, because lipgloss styles are rendered through
// a package-level renderer; setting the profile once here is simpler and more
// reliable than threading a renderer through every call site.
func NewTheme(out io.Writer, opts Options) Theme {
	if opts.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	width := terminalWidth(out)
	plain := opts.Plain || isDumbTerminal() || (width > 0 && width < MinPanelWidth)

	theme := Theme{
		Plain:   plain,
		Width:   width,
		Accent:  tokenAccent,
		Success: tokenSuccess,
		Warning: tokenWarning,
		Danger:  tokenDanger,
		Muted:   tokenMuted,
		Fg:      tokenFg,
		Info:    tokenInfo,
		Symbols: unicodeSymbols,
	}
	if plain {
		theme.Symbols = asciiSymbols
	}
	return theme
}

// isDumbTerminal reports terminals that cannot be trusted with box drawing or
// styled output.
func isDumbTerminal() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	return term == "" || term == "dumb"
}

// terminalWidth returns the width of out when it is a terminal, else 0.
func terminalWidth(out io.Writer) int {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return 0
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

// IsTerminal reports whether stream is backed by an interactive terminal.
// Interactive prompts, spinners and the picker are only meaningful when both
// ends are, so this accepts readers and writers alike.
func IsTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

// ---------------------------------------------------------------------------
// Derived styles. Defined as methods so that every visual decision traces back
// to a single token, and a palette change cannot leave stragglers behind.
// ---------------------------------------------------------------------------

func (t Theme) TitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
}
func (t Theme) LabelStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(t.Muted) }
func (t Theme) ValueStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(t.Fg) }
func (t Theme) MutedStyle() lipgloss.Style  { return lipgloss.NewStyle().Foreground(t.Muted) }
func (t Theme) OkStyle() lipgloss.Style     { return lipgloss.NewStyle().Foreground(t.Success) }
func (t Theme) WarnStyle() lipgloss.Style   { return lipgloss.NewStyle().Foreground(t.Warning) }
func (t Theme) DangerStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(t.Danger) }
func (t Theme) KeyStyle() lipgloss.Style    { return lipgloss.NewStyle().Foreground(t.Info).Bold(true) }

func (t Theme) SubtitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Muted).Italic(!t.Plain)
}

func (t Theme) ActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Success).Bold(true)
}

func (t Theme) CursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
}
