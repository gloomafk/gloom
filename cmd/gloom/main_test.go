package main

import (
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPasswordCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{"-pass", "example-password"},
		{"-server", "irc.gloom.im", "-nick", "tester", "-pass", "bad\r\nQUIT"},
	} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			oldArgs, oldFlags := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args, flag.CommandLine = oldArgs, oldFlags })
			os.Args = append([]string{"gloom"}, args...)
			flag.CommandLine = flag.NewFlagSet("gloom", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			if status := run(); status != 2 {
				t.Fatalf("exit status = %d, want 2", status)
			}
		})
	}
}
