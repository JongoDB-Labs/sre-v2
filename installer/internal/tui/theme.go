// Package tui holds the shared tview theme for srectl's terminal UIs — the
// whiptail-style install wizard and the k9s-style monitor. It owns the global
// tview.Styles palette and the centered-dialog frame both surfaces reuse. The
// blue-on-gray look is the generic newt/ncurses aesthetic; the colors and text
// are original (never Security Onion's).
package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SRE whiptail-style palette: a blue backdrop with a light-gray dialog body.
var (
	// ColorScreen is the full-screen blue backdrop.
	ColorScreen = tcell.NewRGBColor(0, 40, 104) // #002868 deep SRE blue
	// ColorDialog is the light-gray dialog body.
	ColorDialog = tcell.NewRGBColor(198, 198, 198) // #C6C6C6
	// ColorDialogText is the dark text on the gray dialog body.
	ColorDialogText = tcell.ColorBlack
	// ColorBorder is the dialog border + graphics.
	ColorBorder = tcell.ColorBlack
	// ColorAccent is the blue used for titles and the selection background.
	ColorAccent = tcell.NewRGBColor(0, 40, 104)
	// ColorSelectedText is the inverse text on a selected (blue) row.
	ColorSelectedText = tcell.ColorWhite
)

// ApplyTheme sets the global tview styles to the SRE whiptail palette. Call once
// before building any tview primitive.
func ApplyTheme() {
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    ColorDialog,       // dialog bodies are gray
		ContrastBackgroundColor:     ColorAccent,       // selection / active = blue
		MoreContrastBackgroundColor: ColorAccent,
		BorderColor:                 ColorBorder,
		TitleColor:                  ColorAccent,
		GraphicsColor:               ColorBorder,
		PrimaryTextColor:            ColorDialogText,   // dark text on gray
		SecondaryTextColor:          ColorDialogText,
		TertiaryTextColor:           ColorDialogText,
		InverseTextColor:            ColorSelectedText, // text on the blue selection
		ContrastSecondaryTextColor:  ColorSelectedText,
	}
}

// Title returns a title-bar string, e.g. Title("SRE Setup", "0.0.0-dev") →
// "SRE Setup — 0.0.0-dev".
func Title(prefix, version string) string {
	return prefix + " — " + version
}

// CenteredDialog wraps inner in a gray bordered box of size w×h, centered on the
// blue screen backdrop — the reusable whiptail dialog frame. title is shown on
// the box border (already prefixed by the caller, e.g. "SRE Setup — v1 · Posture").
func CenteredDialog(title string, inner tview.Primitive, w, h int) tview.Primitive {
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(inner, 0, 1, true)
	box.SetBorder(true).
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
