package tui

type layoutMode int

const (
	layoutCompact layoutMode = iota
	layoutStandard
	layoutWide
)

type rect struct {
	X int
	Y int
	W int
	H int
}

func (r rect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

type layout struct {
	Width         int
	Height        int
	Mode          layoutMode
	Short         bool
	TooSmall      bool
	Header        rect
	Conversations rect
	Chat          rect
	Activity      rect
	Composer      rect
	Footer        rect
	Overlay       rect
	Send          rect
}

func calculateLayout(width, height int) layout {
	result := layout{Width: width, Height: height, Short: height < 20}
	if width < 50 || height < 16 {
		result.TooSmall = true
		return result
	}
	composerHeight := 5
	if result.Short {
		composerHeight = 3
	}
	result.Header = rect{W: width, H: 1}
	result.Footer = rect{Y: height - 1, W: width, H: 1}
	result.Composer = rect{Y: height - composerHeight - 1, W: width, H: composerHeight}
	content := rect{Y: 1, W: width, H: result.Composer.Y - 1}
	result.Overlay = content
	switch {
	case width >= 132 && height >= 30:
		result.Mode = layoutWide
		result.Conversations = rect{X: 0, Y: content.Y, W: 28, H: content.H}
		result.Activity = rect{X: width - 34, Y: content.Y, W: 34, H: content.H}
		result.Chat = rect{X: 28, Y: content.Y, W: width - 62, H: content.H}
	case width >= 88:
		result.Mode = layoutStandard
		result.Conversations = rect{X: 0, Y: content.Y, W: 26, H: content.H}
		result.Chat = rect{X: 26, Y: content.Y, W: width - 26, H: content.H}
		result.Activity = rect{X: width - 36, Y: content.Y, W: 36, H: content.H}
	default:
		result.Mode = layoutCompact
		result.Chat = content
		result.Conversations = content
		result.Activity = content
	}
	buttonWidth := 10
	result.Send = rect{X: width - buttonWidth - 2, Y: result.Composer.Y + 1, W: buttonWidth + 2, H: 1}
	return result
}
