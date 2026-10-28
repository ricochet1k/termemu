module github.com/ricochet1k/termemu/oracles/ghostty

go 1.24.0

require (
	github.com/ricochet1k/termemu v0.0.0
	github.com/rivo/uniseg v0.4.7
)

require github.com/creack/pty v1.1.24 // indirect

replace github.com/ricochet1k/termemu => ../..
