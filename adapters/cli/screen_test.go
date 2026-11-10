package cli

import (
	"bytes"
	"context"
	"testing"
	"virdeck/core/screenlayout"
	"virdeck/core/service"
)

type screenPositionService struct {
	service.Service
	bounds             screenlayout.Rect
	positioned, normal bool
}

func (s *screenPositionService) List(context.Context) ([]service.Instance, error) {
	return []service.Instance{{ID: "one", Name: "device", State: service.Running}}, nil
}
func (s *screenPositionService) OpenScreen(context.Context, string) error {
	s.normal = true
	return nil
}
func (s *screenPositionService) OpenScreenAt(_ context.Context, _ string, b screenlayout.Rect) error {
	s.positioned = true
	s.bounds = b
	return nil
}
func TestScreenStartupGeometryFlags(t *testing.T) {
	s := &screenPositionService{}
	var out bytes.Buffer
	if err := Run(context.Background(), s, []string{"screen", "-x", "-400", "-y", "40", "-width", "360", "-height", "640", "one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !s.positioned || s.normal || s.bounds != (screenlayout.Rect{X: -400, Y: 40, Width: 360, Height: 640}) {
		t.Fatalf("lost geometry %+v", s)
	}
	bad := &screenPositionService{}
	if err := Run(context.Background(), bad, []string{"screen", "-width", "-1", "one"}, &out); err == nil || bad.positioned || bad.normal {
		t.Fatal("invalid geometry reached projection")
	}
	plain := &screenPositionService{}
	if err := Run(context.Background(), plain, []string{"screen", "one"}, &out); err != nil || !plain.normal || plain.positioned {
		t.Fatalf("plain screen regressed: %v %+v", err, plain)
	}
}
