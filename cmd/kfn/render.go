package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ParhamCh/kfn/internal/manifest"
)

// loadSpec parses the -f file (or stdin when "-") and applies an optional namespace
// override. Shared by render and apply.
func loadSpec(file, namespace string) (*manifest.FunctionSpec, error) {
	if file == "" {
		return nil, fmt.Errorf("missing -f function.yaml")
	}

	var r io.Reader = os.Stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}

	spec, err := manifest.Load(r)
	if err != nil {
		return nil, err
	}
	if namespace != "" {
		spec.Namespace = namespace
	}
	return spec, nil
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	file := fs.String("f", "", "path to function.yaml (\"-\" for stdin)")
	out := fs.String("o", "", "write to this file instead of stdout")
	namespace := fs.String("n", "", "override the target namespace")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := loadSpec(*file, *namespace)
	if err != nil {
		return err
	}

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return spec.Render(w)
}
