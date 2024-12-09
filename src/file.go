package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/awesome-gocui/gocui"
)

var fileCursor int
var FILES []os.DirEntry
var VIEW_FILE_TREE = "fileTree"

func SetFileTreeView(gui *gocui.Gui) error {
	size, err := calculateViewDimensions(gui, 0.35, 0.5)
	if err != nil {
		return errors.Join(errors.New("failed to calculate view dimensions"), err)
	}

	fileTreeView, err := gui.SetView(VIEW_FILE_TREE, size.TopLeftX, size.TopLeftY, size.BottomRightX, size.BottomRightY, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	fileTreeView.Clear()

	FILES, err = os.ReadDir(".")
	if err != nil {
		return err
	}

	for _, file := range FILES {
		fmt.Fprintln(fileTreeView, file.Name())
	}

	if err := bindKeys(gui); err != nil {
		fmt.Println("Error setting key bindings:", err)
		return err
	}

	if _, err := gui.SetCurrentView(VIEW_FILE_TREE); err != nil {
		fmt.Println("Error setting current view:", err)
		return err
	}

	renderFileTree(fileTreeView)
	return nil
}

func bindKeys(gui *gocui.Gui) error {
	var err error
	err = gui.SetKeybinding(VIEW_FILE_TREE, gocui.KeyArrowDown, gocui.ModNone, cursorDown)
	if err != nil {
		return err
	}

	err = gui.SetKeybinding(VIEW_FILE_TREE, gocui.KeyArrowUp, gocui.ModNone, cursorUp)
	if err != nil {
		return err
	}

	err = gui.SetKeybinding(VIEW_FILE_TREE, 'j', gocui.ModNone, cursorDown)
	if err != nil {
		return err
	}

	err = gui.SetKeybinding(VIEW_FILE_TREE, 'k', gocui.ModNone, cursorUp)
	return err
}

func cursorDown(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor < len(FILES)-1 {
		fileCursor++
		renderFileTree(v)
	}
	return nil
}

func cursorUp(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor > 0 {
		fileCursor--
		renderFileTree(v)
	}
	return nil
}

func renderFileTree(view *gocui.View) {
	view.Clear()
	for i, file := range FILES {
		if i == fileCursor {
			fmt.Fprintln(view, "->", file.Name()) // Mark selected file
		} else {
			fmt.Fprintln(view, file.Name())
		}
	}
}

func calculateViewDimensions(gui *gocui.Gui, h_fraction, w_fraction float64) (ViewDimensions, error) {
	maxX, maxY := gui.Size()

	if (h_fraction > 1) || (w_fraction > 1) {
		return ViewDimensions{}, errors.New("fractions must be between 0 and 1")
	}

	width := int(float64(maxX) * w_fraction)
	height := int(float64(maxY) * h_fraction)

	return ViewDimensions{
		TopLeftX:     0,
		TopLeftY:     0,
		BottomRightX: width,
		BottomRightY: height,
	}, nil
}
