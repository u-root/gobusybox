// embedvar generates a Go file containing one variable containing one file.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
)

var (
	pkg     = flag.String("p", "", "Package name")
	file    = flag.String("file", "", "File name to embed")
	o       = flag.String("o", "", "Output file name")
	varName = flag.String("varname", "", "Variable name to use")
)

func main() {
	flag.Parse()

	content, err := ioutil.ReadFile(*file)
	if err != nil {
		log.Fatalf("Failed to read file content: %v", err)
	}

	format := `package %s

var %s = []byte(%s)
`

	if err := ioutil.WriteFile(*o, []byte(fmt.Sprintf(format, *pkg, *varName, strconv.Quote(string(content)))), 0644); err != nil {
		log.Fatalf("Could not write file %s: %v", *o, err)
	}
}
