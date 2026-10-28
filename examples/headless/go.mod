module github.com/ricochet1k/termemu/examples/headless

go 1.25.0

require (
	github.com/ricochet1k/termemu v0.0.0
	github.com/ricochet1k/termemu/examples/internal/raster v0.0.0
)

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/image v0.41.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace (
	github.com/ricochet1k/termemu => ../..
	github.com/ricochet1k/termemu/examples/internal/raster => ../internal/raster
)
