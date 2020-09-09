package log

import (
	"golang.org/x/sys/unix"
)

var SomeDirent = unix.Dirent{}

func Hello() string {
	return "log"
}
