package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/atilarum/wsfold/internal/cli"
)

const (
	ansiRed   = "\x1b[31m"
	ansiBold  = "\x1b[1m"
	ansiReset = "\x1b[0m"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, formatCLIError(err))
		os.Exit(1)
	}
}

func formatCLIError(err error) string {
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "Error: ")

	marker := ansiRed + ansiBold + "✗" + ansiReset
	if strings.HasPrefix(msg, marker+" Error: ") {
		return msg
	}
	if strings.HasPrefix(msg, marker+" ") {
		msg = strings.TrimPrefix(msg, marker+" ")
	}
	return marker + " Error: " + msg
}
