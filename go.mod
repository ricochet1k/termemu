module github.com/ricochet1k/termemu

go 1.14

require (
	github.com/creack/pty v1.1.9
	github.com/creack/termios v0.0.0-20160714173321-88d0029e36a1
	github.com/google/go-cmp v0.4.0
	github.com/kr/pretty v0.2.0 // indirect
	github.com/kr/pty v1.1.8 // indirect
	github.com/limetext/qml-go v0.0.0-20160810010840-e895a291f2aa
	github.com/xo/terminfo v0.0.0-20190125114736-1a4775eeeb62
	golang.org/x/sys v0.0.0-20200202164722-d101bd2416d5 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
)

replace github.com/xo/terminfo => ../terminfo