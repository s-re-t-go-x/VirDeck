//go:build darwin

package screenlayout

// Close presses only the verified window's close button, never kills its PID.
func Close(w Window) error {
	a, err := validatedAX(w)
	if err != nil {
		return err
	}
	defer mac.release(a)
	button, err := axCopy(a, "AXCloseButton")
	if err != nil {
		return err
	}
	defer mac.release(button)
	return axPerform(button, "AXPress")
}
