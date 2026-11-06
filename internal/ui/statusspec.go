package ui

// Reference measurements use CSS px at 1x, JetBrains Mono 13px on a 20px line,
// so one cell is 7.8 by 20 and a 40px status bar is two cell rows. The code
// works in cells and rows, converted from these, to preserve the proportions
// on any font. TestStatusBarMatchesPrototype holds it to them.
const (
	protoCellW   = 7.8
	protoCellH   = 20.0
	protoBarH    = 40.0
	protoLogoW   = 26.0
	protoLogoH   = 24.0
	protoLogoX   = 8.0  // the logo starts 8px in
	protoLogoGap = 5.0  // between the logo and "GLOOM"
	protoGloomW  = 89.6 // the accent segment
	protoIcon    = 16.0 // the icon viewBox, stroke 1.5
	protoButtonW = 30.0 // an icon button, 40px tall
	protoMarker  = 2.0  // the open-view inset underline
)

// The same sizes as fractions of a text row.
const (
	logoRows    = protoLogoH / protoCellH // 1.2 rows tall
	iconBoxRows = protoIcon / protoCellH  // 0.8 rows, centred in its button
	markerRows  = protoMarker / protoCellH
)

// Use the measured Ghostty cell aspect (17 by 37 pixels). Wider canvases
// are shrunk by the terminal; 64 by 120 made images about 14% too small.
const (
	cellPxW  = 55
	cellPxH  = 120
	markerPx = int(cellPxH * markerRows)
)

const (
	// logoCols holds the logo's left padding, the mark and the gap before
	// "GLOOM": (8 + 26 + 5) / 7.8 = 5 cells. With "GLOOM " it makes the accent
	// segment 11 cells, against the prototype's 89.6 / 7.8 = 11.5.
	logoCols = 5
	// iconCols is an icon button: 30 / 7.8 = 3.85 cells, rounded up.
	iconCols = 4
)
