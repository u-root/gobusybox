module github.com/u-root/gobusybox/test/mod5

go 1.15

replace github.com/u-root/gobusybox/test/mod6 => ../mod6

require github.com/u-root/gobusybox/test/mod6 v0.0.0-00010101000000-000000000000
