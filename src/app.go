// This is the main layout file of the application.

package app

import (
	"errors"
	"github.com/awesome-gocui/gocui"
)

func AppLayoutManager(gui *gocui.Gui) error {

	err := SetFileTreeView(gui)

	if err != nil {
		return errors.Join(errors.New("failed to initialize file tree view"), err)
	}

	return nil
}
