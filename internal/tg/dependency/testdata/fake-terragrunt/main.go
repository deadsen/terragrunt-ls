package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	workingDirectory, _ := os.Getwd()
	if filename := os.Getenv("TG_TEST_CWD_FILE"); filename != "" {
		_ = os.WriteFile(filename, []byte(workingDirectory), 0o600)
	}
	if filename := os.Getenv("TG_TEST_ARGS_FILE"); filename != "" {
		_ = os.WriteFile(filename, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	}

	switch os.Getenv("TG_TEST_MODE") {
	case "error":
		_, _ = fmt.Fprintln(os.Stderr, "\x1b[31mBackend initialization required\x1b[0m")
		_, _ = fmt.Fprintln(os.Stderr, "run terragrunt init before resolving outputs")
		os.Exit(1)
	case "invalid":
		_, _ = fmt.Fprintln(os.Stdout, "not-json")
	case "sleep":
		time.Sleep(10 * time.Second)
	default:
		_, _ = fmt.Fprintln(os.Stdout, `{"id":{"value":"123"}}`)
	}
}
