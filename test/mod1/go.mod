module github.com/u-root/gobusybox/test/mod1

go 1.15

replace github.com/u-root/gobusybox/test/mod2 => ../mod2

replace github.com/u-root/gobusybox/test/mod3 => ../mod3

require (
	github.com/u-root/gobusybox/test/mod2 v0.0.0-00010101000000-000000000000
	github.com/u-root/gobusybox/test/mod3 v0.0.0-00010101000000-000000000000 // indirect
	golang.org/x/sys v0.0.0-20200905004654-be1d3432aa8f
)
