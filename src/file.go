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

	if err := setupFileTreeView(gui); err != nil {
		return err
	}

	if err := setupMetadataView(gui); err != nil {
		return err
	}

	if err := bindKeys(gui); err != nil {
		fmt.Println("Error setting key bindings:", err)
		return err
	}

	if _, err := gui.SetCurrentView(VIEW_FILE_TREE); err != nil {
		fmt.Println("Error setting current view:", err)
		return err
	}

	return nil
}

func setupFileTreeView(gui *gocui.Gui) error {
	fileTreeSize, err := CalculateViewDimensions(gui, 0.6, 0.25)
	if err != nil {
		return errors.Join(errors.New("failed to calculate view dimensions"), err)
	}

	fileTreeView, err := gui.SetView(VIEW_FILE_TREE, fileTreeSize.TopLeftX, fileTreeSize.TopLeftY, fileTreeSize.BottomRightX, fileTreeSize.BottomRightY, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	fileTreeView.Clear()

	FILES, err = os.ReadDir(currentPath)
	if err != nil {
		return err
	}

	for _, file := range FILES {
		fmt.Fprintln(fileTreeView, file.Name())
	}

	renderFileTree(fileTreeView)
	return nil
}

func setupMetadataView(gui *gocui.Gui) error {
	fileTreeSize, err := CalculateViewDimensions(gui, 0.6, 0.25)
	if err != nil {
		return err
	}

	metadataSize, err := CalculateViewDimensions(gui, 0.4, 0.25)
	if err != nil {
		return err
	}

	metadataView, err := gui.SetView(VIEW_METADATA, metadataSize.TopLeftX, fileTreeSize.BottomRightY+1, metadataSize.BottomRightX, fileTreeSize.BottomRightY+1+metadataSize.BottomRightY, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}

	metadataView.Clear()
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

// Open the selected file in the editor side panel.
func openFileInEditor(gui *gocui.Gui) error {
	if fileCursor < 0 || fileCursor >= len(FILES) {
		return nil
	}
	fileName := FILES[fileCursor].Name()
	return SetEditorView(gui, filepath.Join(currentPath, fileName))
}

// Render the file tree view.
var expandedDirs = make(map[string]bool)

func renderFileMetadata(view *gocui.View) {
	view.Clear()

	if fileCursor < 0 || fileCursor >= len(displayedEntries) {
		fmt.Fprintln(view, "No file selected.")
		return
	}

	file := displayedEntries[fileCursor]
	fileInfo, err := os.Stat(file.path) // Use full path to get nested file info
	if err != nil {
		fmt.Fprintf(view, "Error: %v\n", err)
		return
	}

	fileType := "File"
	if fileInfo.IsDir() {
		fileType = "Directory"
	}

	size := fileInfo.Size()
	modTime := fileInfo.ModTime().Format(time.RFC1123)

	fmt.Fprintf(view, "File Metadata\n-------------\n")
	fmt.Fprintf(view, "Name     : %s\n", fileInfo.Name())
	fmt.Fprintf(view, "Type     : %s\n", fileType)
	fmt.Fprintf(view, "Size     : %d bytes\n", size)
	fmt.Fprintf(view, "Modified : %s\n", modTime)
	fmt.Fprintf(view, "Path     : %s\n", file.path)
}

var displayedEntries []DisplayedEntry

func renderFileTree(view *gocui.View) {
	view.Clear()
	displayedEntries = []DisplayedEntry{}
	renderDir(currentPath, view, "", true, 0)
}

func renderDir(path string, view *gocui.View, indent string, mainDir bool, level int) {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Fprintf(view, "Error: %v\n", err)
		return
	}

	for _, file := range entries {
		displayedEntries = append(displayedEntries, DisplayedEntry{
			path:  filepath.Join(path, file.Name()),
			name:  file.Name(),
			isDir: file.IsDir(),
			level: level,
		})

		if fileCursor == len(displayedEntries)-1 {
			fmt.Fprintf(view, "%s-> %s\n", indent, file.Name())
		} else {
			fmt.Fprintf(view, "%s   %s\n", indent, file.Name())
		}

		if file.IsDir() && expandedDirs[filepath.Join(path, file.Name())] {
			renderDir(filepath.Join(path, file.Name()), view, indent+"   ", false, level+1)
		}
	}
}

func cursorDown(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor < len(displayedEntries)-1 {
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

func enter(gui *gocui.Gui, v *gocui.View) error {
	if fileCursor < 0 || fileCursor >= len(displayedEntries) {
		return nil
	}

	selectedEntry := displayedEntries[fileCursor]
	if selectedEntry.isDir {
		expandedDirs[selectedEntry.path] = !expandedDirs[selectedEntry.path] // Toggle expanded state
		renderFileTree(v)
		return nil
	}

	return SetEditorView(gui, selectedEntry.path)
}
