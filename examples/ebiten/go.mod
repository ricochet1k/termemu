module github.com/ricochet1k/termemu/examples/ebiten

go 1.23.0

require (
	github.com/hajimehoshi/ebiten/v2 v2.8.8
	github.com/ricochet1k/termemu v0.0.0
	github.com/ricochet1k/termemu/examples/internal/raster v0.0.0
)

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/ebitengine/gomobile v0.0.0-20240911145611-4856209ac325 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/ebitengine/purego v0.8.0 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/image v0.24.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.22.0 // indirect
)

replace (
	github.com/ricochet1k/termemu => ../..
	github.com/ricochet1k/termemu/examples/internal/raster => ../internal/raster
)
