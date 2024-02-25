// gencmddeps generates a command dependency Go file.
package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"text/template"
)

var (
	pkg = flag.String("p", "", "Package name")
	o   = flag.String("o", "", "Output file name")
	tag = flag.String("t", "", "Go build tag for the file")
)

func main() {
	flag.Parse()

	if *tag == "" {
		log.Fatal("Must specify Go build tag")
	}
	if *pkg == "" {
		log.Fatal("Must specify package name")
	}
	if *o == "" {
		log.Fatal("Must specify output file name")
	}
	if flag.NArg() == 0 {
		log.Fatalf("No commands to import given")
	}

	tpl := `//go:build {{.Tag}}

package {{.Package}}

import ({{range .Imports}}
	_ "{{.}}"{{end}}
)
`

	vars := struct {
		Tag     string
		Package string
		Imports []string
	}{
		Tag:     *tag,
		Package: *pkg,
		Imports: flag.Args(),
	}
	t := template.Must(template.New("tpl").Parse(tpl))
	var b bytes.Buffer
	if err := t.Execute(&b, vars); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*o, b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
