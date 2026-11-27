package syncinput

import "math"

// Rect represents a screen rectangle in physical desktop coordinates.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// ContentRect computes the centered video rectangle inside a scrcpy window client area,
// deducting any letterbox or pillarbox black bars caused by mismatched aspect ratio.
func ContentRect(client Rect, deviceW, deviceH int) Rect {
	if client.Width <= 0 || client.Height <= 0 || deviceW <= 0 || deviceH <= 0 {
		return client
	}
	clientRatio := float64(client.Width) / float64(client.Height)
	devRatio := float64(deviceW) / float64(deviceH)

	if math.Abs(clientRatio-devRatio) < 1e-4 {
		return client
	}
	if clientRatio > devRatio {
		// Pillarbox (left/right black bars): height fills client, width shrinks.
		w := int(math.Round(float64(client.Height) * devRatio))
		if w < 1 {
			w = 1
		}
		if w > client.Width {
			w = client.Width
		}
		return Rect{
			X:      client.X + (client.Width-w)/2,
			Y:      client.Y,
			Width:  w,
			Height: client.Height,
		}
	}
	// Letterbox (top/bottom black bars): width fills client, height shrinks.
	h := int(math.Round(float64(client.Width) / devRatio))
	if h < 1 {
		h = 1
	}
	if h > client.Height {
		h = client.Height
	}
	return Rect{
		X:      client.X,
		Y:      client.Y + (client.Height-h)/2,
		Width:  client.Width,
		Height: h,
	}
}

// NormalizePoint maps a host screen point into normalized [0, 1] x [0, 1] coordinates within content.
func NormalizePoint(screenX, screenY int, content Rect) (rx, ry float64, inside bool) {
	if content.Width <= 0 || content.Height <= 0 {
		return 0, 0, false
	}
	if screenX < content.X || screenX >= content.X+content.Width ||
		screenY < content.Y || screenY >= content.Y+content.Height {
		return 0, 0, false
	}
	rx = float64(screenX-content.X) / float64(max(1, content.Width-1))
	if content.Width == 1 {
		rx = 0
	}
	ry = float64(screenY-content.Y) / float64(max(1, content.Height-1))
	if content.Height == 1 {
		ry = 0
	}
	rx = clamp01(rx)
	ry = clamp01(ry)
	return rx, ry, true
}

// DenormalizePoint converts [0, 1] coordinates into pixel coordinates within [0, targetW-1] x [0, targetH-1],
// optionally adding uniform integer micro-jitter in [-jitterPx, +jitterPx].
func DenormalizePoint(rx, ry float64, targetW, targetH, jitterPx int, randIntn func(int) int) (x, y int32) {
	if targetW <= 0 {
		targetW = 1
	}
	if targetH <= 0 {
		targetH = 1
	}
	rx = clamp01(rx)
	ry = clamp01(ry)

	px := int(math.Round(rx * float64(targetW-1)))
	py := int(math.Round(ry * float64(targetH-1)))
	if jitterPx > 0 && randIntn != nil {
		span := 2*jitterPx + 1
		px += randIntn(span) - jitterPx
		py += randIntn(span) - jitterPx
	}
	if px < 0 {
		px = 0
	} else if px >= targetW {
		px = targetW - 1
	}
	if py < 0 {
		py = 0
	} else if py >= targetH {
		py = targetH - 1
	}
	return int32(px), int32(py)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
