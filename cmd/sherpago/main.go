// Command sherpago reads documentation from a sherpa API ("sherpadoc")
// and outputs a documented Go package that exports all functions
// and types referenced in that machine-readable documentation.
//
// Example:
//
// 	sherpadoc MyAPI >myapi.json
// 	sherpago < myapi.json > myapi.ts
package main

import "github.com/mjl-/sherpago"

func main() {
	sherpago.Main()
}
