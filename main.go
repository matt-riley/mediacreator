// mediacreator is a small CLI that lets agents generate media via fal.ai or
// kie.ai and save the finished files to disk.
package main

import (
	"fmt"
	"os"

	"mediacreator/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
