//go:build windows

package screenlayout

func Close(w Window) error {
	if err := validate(w); err != nil {
		return err
	}
	ok, _, err := user32.NewProc("PostMessageW").Call(w.Handle, 0x0010, 0, 0)
	if ok == 0 {
		return nativeError("关闭投屏窗口", err)
	}
	return nil
}
