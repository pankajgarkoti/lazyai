package app

type ViewDimensions struct {
	TopLeftX     int
	TopLeftY     int
	BottomRightX int
	BottomRightY int
}

type DisplayedEntry struct {
	path  string
	name  string
	isDir bool
	level int
}
