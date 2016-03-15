package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var usage = string(`Go# is a tool for rewriting Go# packages into Go packages.

Usage: gosharp [directory path] [-v]

The directory path is an absolute or relative path to a directory containing
a Go# package (comprised of .gos files).

Specify the -v flag to see intermediate files generated for rewriting.

If no directory path is specified, it is assumed to be the current directory.
`)

func main() {
	args, verbose := Remove("-v", os.Args)
	os.Args = args
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		fmt.Println(usage)
		os.Exit(0)
	}
	if len(os.Args) > 2 {
		fmt.Println("gos expects 0 or 1 arguments")
		fmt.Println(usage)
		os.Exit(1)
	}

	var dirpath string
	var err error
	if len(os.Args) == 1 {
		dirpath, err = os.Getwd()
		if err != nil {
			fmt.Println("error getting current directory:", err)
			os.Exit(1)
		}
	} else if len(os.Args) == 2 {
		dirpath, err = filepath.Abs(os.Args[1])
		if err != nil {
			fmt.Println("bad directory path:", err)
			os.Exit(1)
		}
	}
	Rewrite(dirpath, verbose)
}

func Remove(s string, ss []string) ([]string, bool) {
	index := -1
	for i, a := range ss {
		if a == s {
			index = i
		}
	}
	if index == -1 {
		return ss, false
	} else {
		return append(ss[:index], ss[index+1:]...), true
	}
}
