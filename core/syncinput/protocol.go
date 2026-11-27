// Package syncinput captures host mouse input over a master scrcpy window and
// broadcasts normalized touch/scroll/key/text control messages to headless or
// windowed Android instances over scrcpy-server control channels.
package syncinput

import (
	"encoding/binary"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MsgInjectKeycode     uint8 = 0
	MsgInjectText        uint8 = 1
	MsgInjectTouchEvent  uint8 = 2
	MsgInjectScrollEvent uint8 = 3
	MsgSetClipboard      uint8 = 9
)

const (
	ActionDown uint8 = 0
	ActionUp   uint8 = 1
	ActionMove uint8 = 2
)

const (
	PointerIDGenericFinger int64 = -2 // 0xFFFFFFFFFFFFFFFE

	ButtonPrimary uint32 = 1 << 0

	KeyHome      uint32 = 3
	KeyBack      uint32 = 4
	KeyAppSwitch uint32 = 187

	maxInjectTextBytes = 300
	maxClipboardBytes  = 256 * 1024
)

var (
	scrcpyVersionRe = regexp.MustCompile(`scrcpy\s+([0-9]+(?:\.[0-9]+)+)`)
	wmSizeRe        = regexp.MustCompile(`(\d+)\s*x\s*(\d+)`)
)

// EncodeKeycode packs a 14-byte scrcpy INJECT_KEYCODE control message.
func EncodeKeycode(action uint8, keycode, repeat, metaState uint32) []byte {
	buf := make([]byte, 14)
	buf[0] = MsgInjectKeycode
	buf[1] = action
	binary.BigEndian.PutUint32(buf[2:6], keycode)
	binary.BigEndian.PutUint32(buf[6:10], repeat)
	binary.BigEndian.PutUint32(buf[10:14], metaState)
	return buf
}

// EncodeText packs a scrcpy INJECT_TEXT control message, truncating at UTF-8 rune boundaries to <= 300 bytes.
func EncodeText(text string) []byte {
	trimmed := truncateUTF8Bytes(text, maxInjectTextBytes)
	raw := []byte(trimmed)
	buf := make([]byte, 5+len(raw))
	buf[0] = MsgInjectText
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(raw)))
	copy(buf[5:], raw)
	return buf
}

// EncodeSetClipboard packs a scrcpy SET_CLIPBOARD control message (supporting UTF-8/Chinese & immediate paste).
func EncodeSetClipboard(sequence uint64, paste bool, text string) []byte {
	trimmed := truncateUTF8Bytes(text, maxClipboardBytes)
	raw := []byte(trimmed)
	buf := make([]byte, 14+len(raw))
	buf[0] = MsgSetClipboard
	binary.BigEndian.PutUint64(buf[1:9], sequence)
	if paste {
		buf[9] = 1
	}
	binary.BigEndian.PutUint32(buf[10:14], uint32(len(raw)))
	copy(buf[14:], raw)
	return buf
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		cut = i
	}
	// If single rune starts at 0 and exceeds maxBytes, cut is 0; keep within valid rune boundary.
	for cut < len(s) {
		_, size := utf8.DecodeRuneInString(s[cut:])
		if cut+size > maxBytes {
			break
		}
		cut += size
	}
	return s[:cut]
}

// EncodeTouch packs a 32-byte scrcpy INJECT_TOUCH_EVENT control message.
func EncodeTouch(action uint8, pointerID int64, x, y int32, width, height uint16, pressure uint16, actionButton, buttons int32) []byte {
	buf := make([]byte, 32)
	buf[0] = MsgInjectTouchEvent
	buf[1] = action
	binary.BigEndian.PutUint64(buf[2:10], uint64(pointerID))
	binary.BigEndian.PutUint32(buf[10:14], uint32(x))
	binary.BigEndian.PutUint32(buf[14:18], uint32(y))
	binary.BigEndian.PutUint16(buf[18:20], width)
	binary.BigEndian.PutUint16(buf[20:22], height)
	binary.BigEndian.PutUint16(buf[22:24], pressure)
	binary.BigEndian.PutUint32(buf[24:28], uint32(actionButton))
	binary.BigEndian.PutUint32(buf[28:32], uint32(buttons))
	return buf
}

// EncodeScroll packs a 21-byte scrcpy INJECT_SCROLL_EVENT control message.
func EncodeScroll(x, y int32, width, height uint16, hScroll, vScroll float32, buttons int32) []byte {
	buf := make([]byte, 21)
	buf[0] = MsgInjectScrollEvent
	binary.BigEndian.PutUint32(buf[1:5], uint32(x))
	binary.BigEndian.PutUint32(buf[5:9], uint32(y))
	binary.BigEndian.PutUint16(buf[9:11], width)
	binary.BigEndian.PutUint16(buf[11:13], height)
	binary.BigEndian.PutUint16(buf[13:15], uint16(floatToI16FP(hScroll)))
	binary.BigEndian.PutUint16(buf[15:17], uint16(floatToI16FP(vScroll)))
	binary.BigEndian.PutUint32(buf[17:21], uint32(buttons))
	return buf
}

func floatToI16FP(v float32) int16 {
	if math.IsNaN(float64(v)) {
		return 0
	}
	if v >= 1.0 {
		return math.MaxInt16
	}
	if v <= -1.0 {
		return math.MinInt16
	}
	scaled := math.Round(float64(v) * float64(1<<15))
	if scaled >= math.MaxInt16 {
		return math.MaxInt16
	}
	if scaled <= math.MinInt16 {
		return math.MinInt16
	}
	return int16(scaled)
}

// ParseScrcpyVersion extracts the version string from `scrcpy --version` output.
func ParseScrcpyVersion(output string) string {
	m := scrcpyVersionRe.FindStringSubmatch(output)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// ParseWMSize parses `adb shell wm size` output, preferring Override size over Physical size.
func ParseWMSize(output string) (width, height int, ok bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	var physW, physH int
	var hasPhys bool
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		m := wmSizeRe.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		w, errW := strconv.Atoi(m[1])
		h, errH := strconv.Atoi(m[2])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "override") {
			return w, h, true
		}
		if !hasPhys {
			physW, physH, hasPhys = w, h, true
		}
	}
	if hasPhys {
		return physW, physH, true
	}
	return 0, 0, false
}
