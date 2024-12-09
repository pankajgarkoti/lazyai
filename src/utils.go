package app

import (
	"errors"

	"github.com/awesome-gocui/gocui"
)

func GetViewFromName(name string, gui *gocui.Gui) (*gocui.View, error) {
	view, err := gui.View(name)
	if err != nil {
		return nil, errors.Join(errors.New("failed to get view"), err)
	}

	return view, nil
}
