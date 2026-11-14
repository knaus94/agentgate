// agentgate is a quality gate around sandboxed model workers.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// writeOutput keeps data on stdout and propagates errors instead of reporting success
// after a failed serialization or a closed output pipe.
func writeOutput(w io.Writer, value any) error {
	if text, ok := value.(string); ok {
		_, err := io.WriteString(w, text)
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func main() {
	// Design tools own their grammar and output, including contextual help.
	if len(os.Args) > 1 && tools[os.Args[1]] != "" {
		os.Exit(runTool(tools[os.Args[1]], os.Args[2:]))
	}
	args, err := parseArgs(os.Args[1:])
	if err == nil {
		var value any
		var code int
		if value, code, err = run(args); err == nil {
			if value != nil {
				err = writeOutput(os.Stdout, value)
			}
			if err == nil {
				os.Exit(code)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "agentgate: "+err.Error())
	os.Exit(1)
}
