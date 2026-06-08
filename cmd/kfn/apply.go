package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	file := fs.String("f", "", "path to function.yaml (\"-\" for stdin)")
	namespace := fs.String("n", "", "override the target namespace")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := loadSpec(*file, *namespace)
	if err != nil {
		return err
	}

	if err := ensureNamespace(spec.Namespace); err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := spec.Render(&buf); err != nil {
		return err
	}

	apply := exec.Command("kubectl", "apply", "-f", "-")
	apply.Stdin = &buf
	apply.Stdout = os.Stdout
	apply.Stderr = os.Stderr
	if err := apply.Run(); err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}

// ensureNamespace creates the namespace if it does not already exist. Idempotent.
func ensureNamespace(ns string) error {
	if exec.Command("kubectl", "get", "namespace", ns).Run() == nil {
		return nil // already exists
	}
	create := exec.Command("kubectl", "create", "namespace", ns)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("create namespace %q: %w", ns, err)
	}
	return nil
}
