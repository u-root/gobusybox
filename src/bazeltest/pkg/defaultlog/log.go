package defaultlog

import (
	"log"
	"os"

	"golang.org/x/sys/unix"
)

var SomeDirent = unix.Dirent{}

func Default() *log.Logger {
	return log.New(os.Stderr, "", 0)
}
