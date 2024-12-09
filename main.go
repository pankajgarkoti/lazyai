package main

import (
	"github.com/awesome-gocui/gocui"
	app "lazyai/src"
	"log"
)

func quit(*gocui.Gui, *gocui.View) error {
	return gocui.ErrQuit
}

func main() {
	// Initialize the GUI.
	gui, err := gocui.NewGui(gocui.Output256, true)
	if err != nil {
		log.Fatalf("Failed to initialize GUI: %v.", err)
	}

	// Enable mouse support.
	gui.SetManagerFunc(app.AppLayoutManager)
	gui.Cursor = true
	gui.Mouse = true

	defer gui.Close()

	// Exit the application when the user presses Ctrl+C. No matter which view the user is in.
	if err := gui.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		log.Fatalf("Failed to set exit key combination: %v.", err)
	}

	// Start the application main loop.
	if err := gui.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Fatalf("Failed to start GUI main loop: %v.", err)
	}
}
