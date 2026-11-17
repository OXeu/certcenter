package certcenter

import "embed"

// Frontend contains the assets produced by Vite Plus before compiling Go.
//
//go:embed all:.tmp/ui
var Frontend embed.FS

// Brand contains the application icon in sizes for browsers and page headers.
//
//go:embed assets/icon-*.webp
var Brand embed.FS
