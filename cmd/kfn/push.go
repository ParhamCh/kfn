package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func runPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	file := fs.String("f", "", "path to function.yaml (\"-\" for stdin)")
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

	cmd := exec.Command(engine, pushArgs(spec.Image)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s push: %w", engine, err)
	}
	return nil
}

// pushArgs assembles the engine argument vector for pushing image. Kept pure so it can
// be unit-tested.
func pushArgs(image string) []string {
	return []string{"push", image}
}
