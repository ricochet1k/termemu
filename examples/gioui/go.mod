module github.com/ricochet1k/termemu/examples/gioui

go 1.26.0

toolchain go1.26.10

require (
	gioui.org v0.10.3
	github.com/ricochet1k/termemu v0.0.0
	github.com/ricochet1k/termemu/examples/internal/raster v0.0.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/godbus/dbus/v5 v5.2.4 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/exp/shiny v0.0.0-20261014143641-f0251eb4aa3e // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace (
	github.com/ricochet1k/termemu => ../..
	github.com/ricochet1k/termemu/examples/internal/raster => ../internal/raster
)
