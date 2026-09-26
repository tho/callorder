// Command callorder checks function and method order in Go source files.
package main

import (
	"github.com/tho/callorder"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(callorder.NewAnalyzer())
}
