package main

import (
	"slices"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs("harbor.lan/kfn/hello:0.1.0", buildOptions{
		context:    ".",
		dockerfile: "build/Dockerfile",
		funcPkg:    "./examples/hello",
	})
	want := []string{
		"build",
		"--build-arg", "FUNC_PKG=./examples/hello",
		"-t", "harbor.lan/kfn/hello:0.1.0",
		"-f", "build/Dockerfile",
		".",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildArgs = %v, want %v", got, want)
	}
}

func TestPushArgs(t *testing.T) {
	got := pushArgs("harbor.lan/kfn/hello:0.1.0")
	want := []string{"push", "harbor.lan/kfn/hello:0.1.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("pushArgs = %v, want %v", got, want)
	}
}
