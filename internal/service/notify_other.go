//go:build !windows

package service

import (
	"fmt"
	"os"
)

func reportShellError(err error) {
	fmt.Fprintln(os.Stderr, err)
}

func reportShellSuccess(message string) {
	fmt.Println(message)
}
