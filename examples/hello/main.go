// Command hello is a reference function showing the kfn runtime contract.
// Build it like any function: the runtime library handles serving and lifecycle.
package main

import (
	"context"

	"github.com/ParhamCh/kfn/pkg/runtime"
)

func main() {
	runtime.Start(func(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
		name := req.Query.Get("name")
		if name == "" {
			name = "world"
		}
		return runtime.JSON(200, map[string]string{
			"message": "hello " + name,
		})
	})
}
