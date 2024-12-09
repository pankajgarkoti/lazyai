package app

import (
	"fmt"
	"github.com/awesome-gocui/gocui"
	"io/ioutil"
	"log"
)

var VIEW_EDITOR = "editor"

func SetEditorView(gui *gocui.Gui, fileName string) error {
	size, error := calculateEditorViewDimensions(gui)
	if error != nil {
		return fmt.Errorf("failed to calculate editor view dimensions: %v", error)
	}

	editorView, err := gui.SetView(VIEW_EDITOR, size.TopLeftX, size.TopLeftY, size.BottomRightX, size.BottomRightY, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	editorView.Clear()
	content, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Printf("failed to read file %s: %v", fileName, err)
		return err
	}

	fmt.Fprintln(editorView, string(content))
	gui.SetCurrentView(VIEW_EDITOR)

	return nil
}

func calculateEditorViewDimensions(gui *gocui.Gui) (ViewDimensions, error) {
	maxX, maxY := gui.Size()
	editorWidth := int(float64(maxX) * 0.65) // adjust the width as needed
	editorHeight := maxY

	return ViewDimensions{
		TopLeftX:     maxX - editorWidth,
		TopLeftY:     0,
		BottomRightX: maxX - 1,
		BottomRightY: editorHeight - 1,
	}, nil
}
