package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// buildOptions are the inputs to a container build that are not the image reference.
type buildOptions struct {
	context    string // build context directory
	dockerfile string // path to the Dockerfile
	funcPkg    string // Go package to compile (FUNC_PKG build arg)
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	file := fs.String("f", "", "path to function.yaml (\"-\" for stdin)")
	ctx := fs.String("context", ".", "build context directory")
	dockerfile := fs.String("dockerfile", "build/Dockerfile", "path to the Dockerfile")
	funcPkg := fs.String("func", ".", "Go package to compile into the image (FUNC_PKG)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := loadSpec(*file, "")
	if err != nil {
		return err
	}

	engine, err := containerEngine()
	if err != nil {
		return err
	}

	opts := buildOptions{context: *ctx, dockerfile: *dockerfile, funcPkg: *funcPkg}
	cmd := exec.Command(engine, buildArgs(spec.Image, opts)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build: %w", engine, err)
	}
	return nil
}

// buildArgs assembles the engine argument vector for building image from opts. Kept
// pure (no exec) so it can be unit-tested.
func buildArgs(image string, opts buildOptions) []string {
	return []string{
		"build",
		"--build-arg", "FUNC_PKG=" + opts.funcPkg,
		"-t", image,
		"-f", opts.dockerfile,
		opts.context,
	}
}
