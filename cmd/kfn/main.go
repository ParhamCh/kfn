// Command kfn renders and applies the Kubernetes manifests for a function described by
// a function.yaml. It is the deploy-side companion to the kfn runtime library.
//
//	kfn render -f function.yaml [-o out.yaml] [-n namespace]
//	kfn build  -f function.yaml [--func .] [--dockerfile build/Dockerfile] [--context .]
//	kfn push   -f function.yaml
//	kfn apply  -f function.yaml [-n namespace]
//	kfn version
package main

import (
	"fmt"
	"os"
)

// version is overridable at build time: -ldflags "-X main.version=v0.3.0".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "render":
		err = runRender(os.Args[2:])
	case "build":
		err = runBuild(os.Args[2:])
	case "push":
		err = runPush(os.Args[2:])
	case "apply":
		err = runApply(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("kfn %s\n", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "kfn: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "kfn:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `kfn — render and apply Kubernetes manifests for a function

Usage:
  kfn render -f function.yaml [-o out.yaml] [-n namespace]
  kfn build  -f function.yaml [--func .] [--dockerfile build/Dockerfile] [--context .]
  kfn push   -f function.yaml
  kfn apply  -f function.yaml [-n namespace]
  kfn version

Commands:
  render   Print the Deployment + Service YAML for the function.
  build    Build the function's container image (docker/podman) from function.yaml.
  push     Push the function's image to its registry.
  apply    Render and apply to the cluster via kubectl (creates the namespace if needed).
  version  Print the kfn version.
`)
}
