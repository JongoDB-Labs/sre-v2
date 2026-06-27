// Package tui holds the shared tview theme for srectl's terminal UIs — the
// whiptail-style install wizard and the k9s-style monitor. It owns the global
// tview.Styles palette, the centered-dialog frame, and the primitive-styling
// helpers both surfaces reuse. The blue-on-gray look is the generic newt/ncurses
// aesthetic; the colors and text are original. Readability comes first: crisp
// black text on a light dialog, a saturated full-width selection bar, and
// near-white input fields (never dark text on a dark field).
package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SRE whiptail-style palette.
var (
	// ColorScreen is the full-screen blue backdrop.
	ColorScreen = tcell.NewRGBColor(0, 40, 104) // #002868
	// ColorDialog is the light-gray dialog body.
	ColorDialog = tcell.NewRGBColor(208, 208, 208) // #D0D0D0
	// ColorDialogText is the primary (high-contrast) text on the dialog body.
	ColorDialogText = tcell.ColorBlack
	// ColorMuted is secondary / help text on the dialog body.
	ColorMuted = tcell.NewRGBColor(72, 82, 96) // #485260
	// ColorBorder is the dialog frame + graphics.
	ColorBorder = tcell.NewRGBColor(64, 64, 64) // #404040
	// ColorAccent is the blue used for titles, labels, and shortcuts.
	ColorAccent = tcell.NewRGBColor(0, 48, 128) // #003080
	// ColorSelectBg is the selection / focus bar.
	ColorSelectBg = tcell.NewRGBColor(20, 94, 184) // #145EB8
	// ColorSelectText is the high-contrast text on the selection bar.
	ColorSelectText = tcell.ColorWhite
	// ColorFieldBg is the near-white body of an input field.
	ColorFieldBg = tcell.NewRGBColor(247, 247, 247) // #F7F7F7
	// ColorFieldText is the text typed into a field (dark on near-white).
	ColorFieldText = tcell.ColorBlack
	// ColorButtonBg is an unfocused button (focused buttons use the select bar).
	ColorButtonBg = tcell.NewRGBColor(176, 176, 176) // #B0B0B0
)

// ApplyTheme sets the global tview styles to the SRE whiptail palette. Call once
// before building any tview primitive.
func ApplyTheme() {
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    ColorDialog,
		ContrastBackgroundColor:     ColorSelectBg,
		MoreContrastBackgroundColor: ColorSelectBg,
		BorderColor:                 ColorBorder,
		TitleColor:                  ColorAccent,
		GraphicsColor:               ColorBorder,
		PrimaryTextColor:            ColorDialogText,
		SecondaryTextColor:          ColorMuted,
		TertiaryTextColor:           ColorMuted,
		InverseTextColor:            ColorSelectText,
		ContrastSecondaryTextColor:  ColorSelectText,
	}
}

// Title returns a title-bar string, e.g. Title("SRE Setup", "0.0.0-dev") →
// "SRE Setup — 0.0.0-dev".
func Title(prefix, version string) string {
	return prefix + " — " + version
}

// StyleList applies the palette to a radiolist / checklist: muted help text, an
// accent shortcut, and a full-width selection bar (white on select-blue).
func StyleList(l *tview.List) *tview.List {
	return l.
		SetMainTextColor(ColorDialogText).
		SetSecondaryTextColor(ColorMuted).
		SetShortcutColor(ColorAccent).
		SetSelectedTextColor(ColorSelectText).
		SetSelectedBackgroundColor(ColorSelectBg).
		SetHighlightFullLine(true)
}

// StyleForm applies the palette to a form: near-white fields with dark text, and
// buttons that read as buttons (gray idle, select-blue when focused).
func StyleForm(f *tview.Form) *tview.Form {
	f.SetFieldBackgroundColor(ColorFieldBg).
		SetFieldTextColor(ColorFieldText).
		SetLabelColor(ColorDialogText).
		SetButtonBackgroundColor(ColorButtonBg).
		SetButtonTextColor(ColorDialogText)
	f.SetButtonActivatedStyle(tcell.StyleDefault.Background(ColorSelectBg).Foreground(ColorSelectText))
	return f
}

// CenteredDialog wraps inner in a gray bordered box of size w×h, centered on the
// blue screen backdrop — the reusable whiptail dialog frame, with interior
// padding so content never touches the border. title shows on the box border.
func CenteredDialog(title string, inner tview.Primitive, w, h int) tview.Primitive {
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(inner, 0, 1, true)
	box.SetBorder(true).
		SetBorderPadding(1, 1, 2, 2).
		SetTitle(" " + title + " ").
		SetBorderColor(ColorBorder).
		SetTitleColor(ColorAccent).
		SetBackgroundColor(ColorDialog)

	row := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(blueSpacer(), 0, 1, false).
		AddItem(box, w, 0, true).
		AddItem(blueSpacer(), 0, 1, false)
	outer := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(blueSpacer(), 0, 1, false).
		AddItem(row, h, 0, true).
		AddItem(blueSpacer(), 0, 1, false)
	outer.SetBackgroundColor(ColorScreen)
	return outer
}

// blueSpacer is an empty box painted screen-blue, used as centering padding.
func blueSpacer() *tview.Box {
	return tview.NewBox().SetBackgroundColor(ColorScreen)
}
