// getpid is a package that has one external dependency.
package main

import (
	"log"

	"golang.org/x/sys/unix"
)

func main() {
	log.Printf("PID: %d", unix.Getpid())
}
