package app

import (
	"errors"
	"fmt"
	"github.com/awesome-gocui/gocui"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

var fileCursor int
var FILES []fs.DirEntry
var VIEW_FILE_TREE = "filetree"
var VIEW_METADATA = "metadata"
var currentPath = "."

// This function sets up the file tree view and the metadata view.
// This is the main function that initializes the file tree view.
func SetFileTreeView(gui *gocui.Gui) error {
	size, err := calculateViewDimensions(gui, 0.35, 0.5)
	if err != nil {
		return errors.Join(errors.New("failed to calculate view dimensions"), err)
	}

	sizeMetadata, err := calculateViewDimensions(gui, 0.35, 0.1)
	if err != nil {
		return err
	}

	fileTreeView, err := gui.SetView(VIEW_FILE_TREE, size.TopLeftX, size.TopLeftY, size.BottomRightX, size.BottomRightY, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	metadataView, err := gui.SetView(VIEW_METADATA, sizeMetadata.TopLeftX, size.BottomRightY+1, sizeMetadata.BottomRightX, size.BottomRightY+1+int(sizeMetadata.BottomRightY), 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	fileTreeView.Clear()
	metadataView.Clear()

	FILES, err = os.ReadDir(currentPath)
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
	renderFileMetadata(metadataView)
	return nil
}

// Set up keybindings for the file tree view.
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

	err = gui.SetKeybinding(VIEW_FILE_TREE, gocui.KeyEnter, gocui.ModNone, enter)
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

// Handle the down key. Changes the selscted file in the file tree view.
// Also calls the renderFileMetadata function to update the metadata view.
func cursorDown(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor < len(FILES)-1 {
		fileCursor++
		renderFileTree(v)

		metadataView, err := gui.View(VIEW_METADATA)
		if err != nil {
			return err
		}

		renderFileMetadata(metadataView)
	}
	return nil
}

// Handle the up key. Changes the selscted file in the file tree view.
// Also calls the renderFileMetadata function to update the metadata view.
func cursorUp(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor > 0 {
		fileCursor--
		renderFileTree(v)

		metadataView, err := gui.View(VIEW_METADATA)
		if err != nil {
			return err
		}

		renderFileMetadata(metadataView)
	}
	return nil
}

// Highlights the selected file in the file tree view.
// If applicable, opens it in side panel.
func enter(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor < 0 || fileCursor >= len(FILES) {
		return nil
	}

	selectedFile := FILES[fileCursor]
	if selectedFile.IsDir() {
		currentPath = filepath.Join(currentPath, selectedFile.Name())
		fileCursor = 0
		return SetFileTreeView(gui)
	}

	return openFileInEditor(gui)
}

// Open the selected file in the editor side panel.
func openFileInEditor(gui *gocui.Gui) error {
	if fileCursor < 0 || fileCursor >= len(FILES) {
		return nil
	}
	fileName := FILES[fileCursor].Name()
	return SetEditorView(gui, filepath.Join(currentPath, fileName))
}

// Render the file tree view.
func renderFileTree(view *gocui.View) {
	view.Clear()
	for i, file := range FILES {
		prefix := "  "
		if file.IsDir() {
			prefix = "->"
		}
		if i == fileCursor {
			fmt.Fprintln(view, "->", prefix, file.Name()) // Mark selected file
		} else {
			fmt.Fprintln(view, prefix, file.Name())
		}
	}
}

func renderFileMetadata(view *gocui.View) {
	view.Clear()
	if fileCursor < 0 || fileCursor >= len(FILES) {
		return
	}

	file := FILES[fileCursor]
	fileInfo, err := file.Info()
	if err != nil {
		fmt.Fprintf(view, "Error: %v\n", err)
		return
	}

	size := fileInfo.Size()
	modTime := fileInfo.ModTime().Format(time.RFC1123)
	fmt.Fprintf(view, "Name: %s\nSize: %d bytes\nModified: %s\n", fileInfo.Name(), size, modTime)
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
