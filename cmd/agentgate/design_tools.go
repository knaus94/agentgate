package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	designkit "github.com/knaus94/agentgate/design-kit"
)

// tools are the design tools: Node programs that own their arguments and their output.
var tools = map[string]string{"figma": "figma.mjs", "layout": "check.mjs", "fixtures": "graphql-fixtures.mjs"}

func runTool(script string, args []string) int {
	dir, err := designkit.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentgate: "+err.Error())
		return 1
	}
	cmd := exec.Command("node", append([]string{filepath.Join(dir, "bin", script)}, args...)...)
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentgate: "+err.Error())
		return 1
	}
	cmd.Env = append(os.Environ(), "AGENTGATE_EXECUTABLE="+self)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "agentgate: the design tools need Node: "+err.Error())
		return 1
	}
	return 0
}
