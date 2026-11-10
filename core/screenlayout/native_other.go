//go:build !windows && !darwin

package screenlayout

func Displays() ([]Display, error)  { return nil, ErrUnsupported }
func Windows() ([]Window, error)    { return nil, ErrUnsupported }
func Place(Window, Rect) error      { return ErrUnsupported }
func Activate(Window) error         { return ErrUnsupported }
func SetTitle(Window, string) error { return ErrUnsupported }
