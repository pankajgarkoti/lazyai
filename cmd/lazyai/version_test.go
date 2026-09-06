package main

import "testing"

func TestVersionWithoutTerminal(t *testing.T) {
	for _, arg := range []string{"--version", "-version", "version"} {
		t.Run(arg, func(t *testing.T) {
			output, err := captureStdout(t, func() error { return run([]string{arg}) })
			if err != nil {
				t.Fatal(err)
			}
			if output != "lazyai "+version+"\n" || version != "0.2.3" {
				t.Fatalf("version output = %q", output)
			}
		})
	}
}
