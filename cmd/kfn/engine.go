package main

import (
	"fmt"
	"os"
	"os/exec"
)

// containerEngine picks the container CLI to shell out to for build and push.
// KFN_CONTAINER_ENGINE overrides the choice; otherwise docker is preferred (it is the
// primary interface on the target hosts, commonly backed by podman) with podman as a
// fallback.
func containerEngine() (string, error) {
	if e := os.Getenv("KFN_CONTAINER_ENGINE"); e != "" {
		if path, err := exec.LookPath(e); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("KFN_CONTAINER_ENGINE=%q not found on PATH", e)
	}
	for _, e := range []string{"docker", "podman"} {
		if path, err := exec.LookPath(e); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no container engine found: install docker or podman (or set KFN_CONTAINER_ENGINE)")
}
