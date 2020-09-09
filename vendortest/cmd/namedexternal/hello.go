package main

import (
	"log"

	"github.com/u-root/gobusybox/vendortest/pkg/defaultlog"
)

var SomeDirent = defaultlog.SomeDirent

var l = defaultlog.Default()

func main() {
	log.Printf("rdonly")
}
