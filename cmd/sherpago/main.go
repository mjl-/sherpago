// Command sherpago reads documentation from a sherpa API ("sherpadoc")
// and outputs a documented Go package that exports all functions
// and types referenced in that machine-readable documentation.
//
// Example:
//
// 	sherpadoc MyAPI >myapi.json
// 	sherpago mypkg http://example.org/myapi/ < myapi.json > myapi.go
package main

import (
	"flag"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/mjl-/sherpago"
)

func check(err error, action string) {
	if err != nil {
		log.Fatalf("%s: %s\n", action, err)
	}
}

func main() {
	log.SetFlags(0)
	flag.Usage = func() {
		log.Println("sherpago packageName baseURL")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		log.Print("bad parameters")
		flag.Usage()
		os.Exit(2)
	}
	packageName := args[0]
	baseURL := args[1]

	if packageName == "" {
		log.Fatalln("invalid empty package name")
	}
	_, err := url.Parse(baseURL)
	check(err, "parsing base URL")
	if !strings.HasSuffix(baseURL, "/") {
		log.Fatalf("bad baseURL %q: must end with a slash\n", baseURL)
	}

	err = sherpago.Generate(os.Stdin, os.Stdout, packageName, baseURL)
	check(err, "generating go client package")
}
