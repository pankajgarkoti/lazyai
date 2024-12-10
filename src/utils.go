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

func CalculateViewDimensions(gui *gocui.Gui, h_fraction, w_fraction float64) (ViewDimensions, error) {
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
