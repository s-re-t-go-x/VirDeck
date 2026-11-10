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

// IconCLISVG is the resolution-independent CLI tool mark (cool slate gray, lower saturation).
//
//go:embed icon-cli.svg
var IconCLISVG []byte

// IconCLIPNG is the rasterized CLI tool icon.
//
//go:embed icon-cli.png
var IconCLIPNG []byte

