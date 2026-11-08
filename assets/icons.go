package assets

import _ "embed"

//go:generate go run ../scripts/icongen

// IconSVG is the full-color, resolution-independent application mark.
//
//go:embed icon.svg
var IconSVG []byte

// IconPNG is used by native window and taskbar icons.
//
//go:embed icon.png
var IconPNG []byte
