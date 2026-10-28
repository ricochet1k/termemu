module github.com/ricochet1k/termemu/examples/internal/raster

go 1.26.0

toolchain go1.26.10

require (
	github.com/ricochet1k/termemu v0.0.0
	github.com/rivo/uniseg v0.4.7
	golang.org/x/image v0.47.0
)

require (
	github.com/creack/pty v1.1.24 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/ricochet1k/termemu => ../../..
