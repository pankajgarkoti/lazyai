package main

import "testing"

func TestVersionWithoutTerminal(t *testing.T) {
	for _, arg := range []string{"--version", "-version", "version"} {
		t.Run(arg, func(t *testing.T) {
			output, err := captureStdout(t, func() error { return run([]string{arg}) })
			if err != nil {
				t.Fatal(err)
			}
			if output != "lazyai 0.1.0\n" {
				t.Fatalf("version output = %q", output)
			}
		})
	}
}
