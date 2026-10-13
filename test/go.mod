module github.com/u-root/gobusybox/test

go 1.25.0

require github.com/u-root/gobusybox/src v0.0.0-20211230232531-fcf634c39c0e

require (
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
)

// all_test.go calls golang.Default().Version() to decide which cases to skip.
// Without this, that call resolves against the published pseudo-version above
// rather than the tree being tested, so the harness runs four-year-old code
// while claiming to test this checkout.
replace github.com/u-root/gobusybox/src => ../src
