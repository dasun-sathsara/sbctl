// +build ignore

// This program generates tray icon PNG files for sbctl-tray.
// Run with: go run cmd/sbctl-tray/gen_icons/main.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func main() {
	dir := filepath.Join("cmd", "sbctl-tray", "assets")
	os.MkdirAll(dir, 0o755)

	// Green = running
	generateIcon(filepath.Join(dir, "active.png"), color.RGBA{76, 175, 80, 255}, 64)
	// Gray = stopped
	generateIcon(filepath.Join(dir, "inactive.png"), color.RGBA{158, 158, 158, 255}, 64)
	// Red = error
	generateIcon(filepath.Join(dir, "error.png"), color.RGBA{244, 67, 54, 255}, 64)
}

func generateIcon(path string, fill color.RGBA, size int) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size) / 2.0
	radius := center * 0.75
	borderWidth := center * 0.08

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - center + 0.5
			dy := float64(y) - center + 0.5
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist <= radius-borderWidth {
				// Inner fill
				img.SetRGBA(x, y, fill)
			} else if dist <= radius {
				// Anti-aliased edge
				alpha := 1.0 - (dist-(radius-borderWidth))/borderWidth
				if alpha < 0 {
					alpha = 0
				}
				img.SetRGBA(x, y, color.RGBA{
					R: fill.R,
					G: fill.G,
					B: fill.B,
					A: uint8(float64(fill.A) * alpha),
				})
			}
			// else transparent
		}
	}

	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
